#!/usr/bin/env python3
"""Isolated installed-package NM L3 viability scenario (never production).

Must run as root *inside* systemd-nspawn on the synthetic guest's actual Xray
profile and uplink. The parent harness owns disposal and baseline inspection.
All stdout is closed-schema result labels; subprocess logs stay private.
"""
from __future__ import annotations

import errno
import ipaddress
import json
import os
import socket
from pathlib import Path
import signal
import subprocess
import sys
import time
import uuid

from nm_vpn_l3_contract import GuestVPNConfig, config_dicts, vpn_profile_settings
from nm_vpn_l3_runtime_guard import validate_xray_tun
from nm_vpn_underlay_guest import UnderlayEvidence, UnderlayPlan, select_underlay

ROOT = Path('/tmp/podlaz-hosted-synthetic-tun')
INTENT = Path('/run/podlaz-nm-l3/intent.json')
SERVICE = 'org.freedesktop.NetworkManager.podlazl3research'
VPN = 'podlaz-guest-nm-l3'
TABLE = 'podlaz_nm_l3_guest'  # Owned exclusively by this disposable guest.
IPV6_GUEST_CIDR = '2001:db8:460::2/64'
IPV6_HOST_PEER = '2001:db8:460::1'


def _numeric_table(table: object) -> int:
    return {'local':255, 'main':254, 'default':253}.get(str(table), int(table) if str(table).isdigit() else -1)


def verify_route_selection(main_defaults: object, ordinary: object, marked: object,
                           uplink: str, mark: int) -> None:
    """Assert actual kernel selections, not proxy metrics or NM CLI labels."""
    if not isinstance(main_defaults, list) or not isinstance(ordinary, list) or not isinstance(marked, list) or len(ordinary) != 1 or len(marked) != 1:
        raise ValueError('incomplete kernel route selection')
    valid = [r for r in main_defaults if isinstance(r, dict) and r.get('dst') == 'default' and _numeric_table(r.get('table',254)) == 254]
    vpn = [r for r in valid if r.get('dev') == 'podlaz0']
    underlay = [r for r in valid if r.get('dev') == uplink]
    if len(vpn) != 1 or len(underlay) != 1:
        raise ValueError('missing real main-table VPN/uplink default')
    vpn_metric, uplink_metric = vpn[0].get('metric'), underlay[0].get('metric')
    if type(vpn_metric) is not int or type(uplink_metric) is not int or vpn_metric >= uplink_metric:
        raise ValueError('VPN default is not preferred')
    if ordinary[0].get('dev') != 'podlaz0' or marked[0].get('dev') != uplink or str(marked[0].get('mark')) not in (str(mark),hex(mark)):
        raise ValueError('unprotected ordinary egress or looping Xray transport')


def verify_owned_underlay(plan: UnderlayPlan, routes: object, rules: object) -> None:
    """Refuse destructive cleanup without exact currently observed identities."""
    if not isinstance(routes, list) or not isinstance(rules, list):
        raise ValueError('incomplete underlay ownership inspection')
    expected_routes = {
        (plan.connected_route[0], plan.connected_route[1], '', 'link', plan.table),
        ('default', plan.default_route[2], plan.default_route[1], '', plan.table),
    }
    observed = set()
    for route in routes:
        if not isinstance(route, dict):
            raise ValueError('invalid observed underlay route')
        entry = (route.get('dst'), route.get('dev'), route.get('gateway',''),
                 route.get('scope',''), _numeric_table(route.get('table',plan.table)))
        observed.add(entry)
    if len(routes) != 2 or observed != expected_routes:
        raise ValueError('foreign or mutated underlay routes')
    if len(rules) != 1 or not isinstance(rules[0], dict):
        raise ValueError('underlay rule is absent or foreign')
    rule=rules[0]
    def to_int(value: object) -> int:
        if type(value) is int:
            return value
        if isinstance(value,str):
            return int(value,0)
        return -1
    if (rule.get('priority') != plan.rule_priority or
        _numeric_table(rule.get('table',rule.get('lookup',-1))) != plan.table or
        to_int(rule.get('fwmark')) != plan.tunnel_mark or
        to_int(rule.get('fwmask',0xffffffff)) != 0xffffffff):
        raise ValueError('underlay RPDB rule differs from exact ownership')




def physical_unmarked_drop_packets(snapshot: object, *, marker: str = 'podlaz_nm_l3_unmarked') -> int:
    """Require the exact guest-only output drop counter as packet evidence."""
    if not isinstance(snapshot, dict) or not isinstance(snapshot.get('nftables'), list):
        raise ValueError('incomplete guest firewall evidence')
    matches = []
    for item in snapshot['nftables']:
        if not isinstance(item, dict) or not isinstance(item.get('rule'), dict):
            continue
        rule = item['rule']
        if (rule.get('table'), rule.get('chain'), rule.get('family'), rule.get('comment')) != (
                TABLE, 'egress', 'inet', marker):
            continue
        expr = rule.get('expr')
        if not isinstance(expr, list) or not any(isinstance(x, dict) and 'drop' in x for x in expr):
            raise ValueError('unmarked physical drop rule was changed')
        counters = [x['counter'].get('packets') for x in expr
                    if isinstance(x, dict) and isinstance(x.get('counter'), dict)]
        if len(counters) != 1 or type(counters[0]) is not int or counters[0] < 0:
            raise ValueError('unmarked physical drop counter unavailable')
        matches.append(counters[0])
    if len(matches) != 1:
        raise ValueError('unmarked physical drop rule identity ambiguous')
    return matches[0]


def exact_owned_nm_profile_present(rows: object, profile_uuid: str) -> bool:
    """Identify only this ephemeral VPN profile; reject name/UUID collisions."""
    if not isinstance(rows, list):
        raise ValueError('invalid NM profile inventory')
    found = 0
    for row in rows:
        if not isinstance(row, tuple) or len(row) != 2 or any(not isinstance(x, str) or not x for x in row):
            raise ValueError('malformed NM profile inventory')
        observed_uuid, name = row
        if observed_uuid == profile_uuid or name == VPN:
            if observed_uuid != profile_uuid or name != VPN:
                raise ValueError('foreign NM profile collision')
            found += 1
    if found > 1:
        raise ValueError('duplicate NM VPN profile authority')
    return found == 1


def _owned_nm_profile_present(profile_uuid: str, active: bool = False) -> bool:
    args = ['nmcli', '-t', '-f', 'UUID,NAME', 'connection', 'show']
    if active:
        args.append('--active')
    rows = []
    for line in run(*args, output=True).splitlines():
        uuid_text, separator, name = line.partition(':')
        if not separator:
            raise ValueError('invalid NM profile row')
        rows.append((uuid_text, name))
    return exact_owned_nm_profile_present(rows, profile_uuid)


def run(*args: str, timeout: int = 20, output: bool = False) -> str:
    done = subprocess.run(args, capture_output=True, text=True, check=True, timeout=timeout)
    return done.stdout if output else ''


def ip_json(*args: str) -> list[dict]:
    payload = json.loads(run('ip','-j',*args,output=True))
    if not isinstance(payload, list):
        raise ValueError('invalid kernel inventory')
    return payload


def _is_synthetic_guest() -> None:
    if os.geteuid() != 0 or Path('/run/systemd/container').read_text().strip() != 'systemd-nspawn':
        raise ValueError('disposable root nspawn only')
    if Path('/run/podlaz/network-session-continuation.json').exists():
        raise ValueError('legacy Podlaz session still has L3 authority')
    if Path('/sys/class/net/podlaz0').exists():
        raise ValueError('TUN identity already occupied')
    if run('nmcli','-t','-f','NAME','connection','show', output=True).splitlines().count(VPN):
        raise ValueError('VPN profile identity collision')


def _underlay_evidence() -> UnderlayEvidence:
    defaults = ip_json('-4','route','show','table','main','default')
    if len(defaults) != 1 or defaults[0].get('dev') != 'host0':
        raise ValueError('unsupported uplink default baseline')
    metric=defaults[0].get('metric')
    if type(metric) is not int:
        raise ValueError('uplink default metric unqualified')
    link = ip_json('-4','route','show','dev','host0','scope','link')
    networks=[r['dst'] for r in link if 'dst' in r and '/' in r['dst']]
    if len(networks)!=1:
        raise ValueError('uplink connected-route identity ambiguous')
    gw=defaults[0].get('gateway')
    if not gw:
        raise ValueError('uplink gateway absent')
    rules = ip_json('-4','rule','show')
    rpdb=tuple((r['priority'],_numeric_table(r.get('table',r.get('lookup',-1))),
                int(str(r.get('fwmark',0)).split('/')[0],0) if isinstance(r.get('fwmark'),str) else r.get('fwmark',0),
                int(str(r.get('fwmask',0)),0) if isinstance(r.get('fwmask'),str) else r.get('fwmask',0)) for r in rules)
    occupied=frozenset(_numeric_table(r.get('table',254)) for r in ip_json('-4','route','show','table','all'))
    return UnderlayEvidence('host0',gw,networks[0],metric,rpdb,occupied)


def _xray_marked_config(path: Path, expected_mark: int) -> None:
    if not path.is_file() or path.is_symlink() or path.stat().st_mode & 0o077:
        raise ValueError('private Xray config missing')
    config=json.loads(path.read_text())
    # The synthetic vmess provider generates its own sockopt mark; no fake
    # marks are injected to make this fixture pass.
    outbounds=config.get('outbounds')
    marks=[o.get('streamSettings',{}).get('sockopt',{}).get('mark') for o in outbounds if isinstance(o,dict)] if isinstance(outbounds,list) else []
    if expected_mark not in marks:
        raise ValueError('generated Xray provider is not bound to allocated egress mark')


def _write_private_intent(gateway: str, index: int) -> GuestVPNConfig:
    config=GuestVPNConfig('podlaz0',gateway,'198.18.0.17/32',('1.1.1.1',))
    config_dicts(config)
    INTENT.parent.mkdir(mode=0o700,parents=True,exist_ok=False)
    fd=os.open(INTENT,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'w') as stream:
        json.dump({'schema':'podlaz.nm-l3-research.v1','owner':'podlaz-guest-nm-l3',
                   'tun_ifindex':index,'tun_name':config.interface,
                   'external_gateway':config.gateway,'tun_address':config.address,
                   'dns':list(config.dns)},stream)
    return config


def _nft_protect(mark: int) -> None:
    run('nft','add','table','inet',TABLE)
    run('nft','add','chain','inet',TABLE,'egress','{ type filter hook output priority -150; policy accept; }')
    # With IPv6 VPN routing unqualified, block any physical IPv6 output,
    # including an additional foreign interface or a marked IPv6 socket.
    run('nft','add','rule','inet',TABLE,'egress','meta','nfproto','ipv6',
        'oifname','!=','lo','oifname','!=','podlaz0','counter','drop',
        'comment','podlaz_nm_l3_ipv6')
    run('nft','add','rule','inet',TABLE,'egress','meta','nfproto','ipv4',
        'oifname','host0','meta','mark','!=',str(mark),'counter','drop',
        'comment','podlaz_nm_l3_unmarked')



def _physical_drop_snapshot() -> int:
    evidence = json.loads(run('nft', '-j', 'list', 'chain', 'inet', TABLE, 'egress', output=True))
    return physical_unmarked_drop_packets(evidence)


def _stage(label: str) -> None:
    print('nm-l3-progress=' + label, flush=True)



def _send_ipv6_probe(peer: str) -> str:
    """Attempt a genuine IPv6 packet; output DROP can synchronously return EPERM.

    This is not proof of protection by itself. The caller MUST subsequently
    observe a strict increment of its dedicated nftables drop counter.
    Non-firewall socket errors are still fatal.
    """
    try:
        with socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as sock:
            sock.sendto(b'guest-ipv6-no-leak', (peer, 5353))
    except OSError as exc:
        if exc.errno != errno.EPERM:
            raise
        return 'firewall-denied'
    return 'sent'


def _prove_ipv6_no_leak() -> None:
    """Make host0 genuinely IPv6-capable, then prove netfilter drops a packet."""
    sysctl = Path('/proc/sys/net/ipv6/conf/host0/disable_ipv6')
    _stage('ipv6-sysctl')
    original = sysctl.read_text(encoding='ascii').strip()
    if original not in ('0', '1'):
        raise ValueError('unknown IPv6 guest uplink state')
    addr_installed = False
    try:
        # The outer synthetic veth peer has RFC3849 2001:db8:460::1.
        if original == '1':
            sysctl.write_text('0\n', encoding='ascii')
        run('ip', '-6', 'address', 'add', IPV6_GUEST_CIDR, 'dev', 'host0')
        addr_installed = True
        _stage('ipv6-route')
        routes = ip_json('-6', 'route', 'get', IPV6_HOST_PEER)
        if len(routes) != 1 or routes[0].get('dev') != 'host0':
            raise ValueError('IPv6 probe lacks a real physical path')
        snapshot = json.loads(run('nft','-j','list','chain','inet',TABLE,'egress',output=True))
        _stage('ipv6-counter')
        before = physical_unmarked_drop_packets(snapshot, marker='podlaz_nm_l3_ipv6')
        _stage('ipv6-send')
        _send_ipv6_probe(IPV6_HOST_PEER)
        _stage('ipv6-observe')
        time.sleep(0.2)
        snapshot = json.loads(run('nft','-j','list','chain','inet',TABLE,'egress',output=True))
        observed = physical_unmarked_drop_packets(snapshot, marker='podlaz_nm_l3_ipv6')
        if observed <= before:
            _stage('ipv6-no-increment')
            raise ValueError('physically routable IPv6 packet escaped nft blocking')
    finally:
        if addr_installed:
            run('ip', '-6', 'address', 'del', IPV6_GUEST_CIDR, 'dev', 'host0')
        if sysctl.read_text(encoding='ascii').strip() != original:
            sysctl.write_text(original + '\n', encoding='ascii')


def _qualify_uplink_churn(xray: subprocess.Popen) -> None:
    """Prove fail-closed physical egress while the NM-owned uplink churns."""
    if xray.poll() is not None:
        raise ValueError('Xray exited before uplink churn')
    _stage('uplink-down')
    before = _physical_drop_snapshot()
    run('ip','link','set','dev','host0','down')
    try:
        if _physical_drop_snapshot() < before:
            raise ValueError('physical barrier was lost during uplink loss')
    finally:
        run('ip','link','set','dev','host0','up')
    _stage('uplink-reconnect')
    run('nmcli','-w','35','connection','up','synthetic-uplink',
        'ifname','host0',timeout=45)
    if 'synthetic-uplink' not in run(
            'nmcli','-t','-f','NAME','connection','show','--active',
            output=True).splitlines():
        raise ValueError('synthetic NM uplink did not recover')
    _prove_physical_drop()
    if xray.poll() is not None:
        raise ValueError('tracked Xray died during uplink churn')


def _prove_physical_drop() -> None:
    """A direct host0 SYN must hit the real unmarked-egress nft counter."""
    before = _physical_drop_snapshot()
    attempt = subprocess.run(
        ('curl', '-4', '--noproxy', '*', '--interface', 'host0',
         '--connect-timeout', '3', '--max-time', '5', '--fail', '-sS',
         'http://172.31.253.1:9/'), capture_output=True, text=True, timeout=10)
    if attempt.returncode == 0:
        raise ValueError('ordinary egress escaped guest physical barrier')
    if _physical_drop_snapshot() <= before:
        raise ValueError('no observed packet blocked at physical uplink')


def _qualify_failure_protection(xray: subprocess.Popen) -> None:
    _stage('core-preflight')
    _prove_physical_drop()
    _prove_ipv6_no_leak()
    if xray.poll() is not None:
        raise ValueError('Xray already failed before crash qualification')
    _stage('core-stop-request')
    xray.terminate()
    xray.wait(timeout=12)
    _stage('core-stopped')
    _prove_physical_drop()
    _prove_ipv6_no_leak()
    # Loss/restart of NM must never open direct physical IPv4/IPv6 egress.
    _stage('nm-restart-request')
    run('systemctl', 'restart', 'NetworkManager.service', timeout=75)
    _stage('nm-restarted')
    _prove_physical_drop()
    _prove_ipv6_no_leak()


def _start_underlay(p: UnderlayPlan) -> None:
    run('ip','-4','route','add',p.connected_route[0],'dev',p.connected_route[1],'scope','link','table',str(p.table))
    run('ip','-4','route','add','default','via',p.default_route[1],'dev',p.default_route[2],'table',str(p.table))
    run('ip','-4','rule','add','pref',str(p.rule_priority),'fwmark',str(p.tunnel_mark),'lookup',str(p.table))


def _qualify_foreign_profile_collision() -> None:
    """A foreign UUID with our display name must block adoption pre-apply."""
    foreign_uuid = str(uuid.uuid4())
    candidate_uuid = str(uuid.uuid4())
    run('nmcli', 'connection', 'add', 'save', 'no', 'type', 'vpn',
        'con-name', VPN, 'vpn.service-type', SERVICE,
        'connection.uuid', foreign_uuid, 'connection.autoconnect', 'no')
    try:
        try:
            _owned_nm_profile_present(candidate_uuid)
        except ValueError:
            pass
        else:
            raise ValueError('foreign profile name was mistaken for owned UUID')
        if _owned_nm_profile_present(foreign_uuid, active=True):
            raise ValueError('foreign collision unexpectedly activated')
    finally:
        # This UUID was created by this isolated guest fixture; never delete
        # arbitrary profile objects merely because the names resemble ours.
        run('nmcli', 'connection', 'delete', foreign_uuid)
    if _owned_nm_profile_present(foreign_uuid):
        raise ValueError('foreign collision fixture leaked')


def _qualify_invalid_ip4_apply() -> None:
    """NM must reject real malformed Ip4Config with protected underlay armed."""
    rejected = INTENT.parent / 'reject-ip4-apply'
    emitted = INTENT.parent / 'bad-ip4-emitted'
    broken_uuid = str(uuid.uuid4())
    fd = os.open(rejected, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    os.close(fd)
    try:
        try:
            _activate_ephemeral_profile(10, broken_uuid)
        except subprocess.CalledProcessError:
            pass
        else:
            raise ValueError('NM accepted an invalid 33-bit IPv4 prefix')
        if not emitted.is_file() or emitted.is_symlink():
            raise ValueError('real malformed Ip4Config was never emitted')
        if _owned_nm_profile_present(broken_uuid, active=True):
            raise ValueError('bad IP4 config unexpectedly became active')
        if any(item.get('family') == 'inet'
               for link in ip_json('address', 'show', 'dev', 'podlaz0')
               for item in link.get('addr_info', [])):
            raise ValueError('rejected NM IP4 applied a partial TUN address')
        if any(route.get('dev') == 'podlaz0'
               for route in ip_json('-4', 'route', 'show', 'table', 'main')):
            raise ValueError('rejected NM IP4 left a VPN route')
        _prove_physical_drop()
    finally:
        rejected.unlink(missing_ok=True)
        emitted.unlink(missing_ok=True)
        if _owned_nm_profile_present(broken_uuid, active=True):
            run('nmcli', '-w', '20', 'connection', 'down', broken_uuid, timeout=25)
        if _owned_nm_profile_present(broken_uuid):
            run('nmcli', 'connection', 'delete', broken_uuid)


def _activate_ephemeral_profile(metric: int, profile_uuid: str) -> None:
    expected=vpn_profile_settings(SERVICE,VPN,
                                  profile_uuid,metric)
    # New guest service has a distinct bus identity; pure contract above only
    # validates deterministic NM semantics and a specific private fixture.
    run('nmcli','connection','add','save','no','type','vpn','con-name',VPN,
        'vpn.service-type',SERVICE,'connection.uuid',profile_uuid,
        'connection.autoconnect','no','ipv4.method','auto',
        'ipv4.route-table','254','ipv4.never-default','no',
        'ipv4.auto-route-ext-gw','no','ipv4.route-metric',str(metric),
        'ipv4.dns-priority','-30000','ipv4.dns-search','~.',
        'ipv6.method','ignore','ipv6.never-default','yes',
        'ipv6.auto-route-ext-gw','no')
    if not expected['ipv4']['auto-route-ext-gw'] is False:
        raise ValueError('unexpected NM guest profile')
    if not _owned_nm_profile_present(profile_uuid):
        raise ValueError('ephemeral NM profile is not owned')
    run('nmcli','-w','40','connection','up',profile_uuid,'ifname','host0',timeout=45)
    if not _owned_nm_profile_present(profile_uuid, active=True):
        raise ValueError('guest NM VPN not active')


def _verify_paths(mark: int) -> None:
    verify_route_selection(
        ip_json('-4','route','show','table','main','default'),
        ip_json('-4','route','get','1.1.1.1'),
        ip_json('-4','route','get','1.1.1.1','mark',str(mark)),
        'host0',mark)
    run('resolvectl','flush-caches')
    run('timeout','30','getent','ahostsv4','example.com',timeout=35)
    run('timeout','35','curl','-4','-fsS','-o','/dev/null','https://example.com/',timeout=40)


def _natural_full(xray: subprocess.Popen) -> None:
    # Do not force NetworkManager connectivity checks: the observed state
    # must outlive more than one real periodic interval in stock NM.
    observed=0
    for _ in range(660):
        if xray.poll() is not None:
            raise ValueError('tracked Xray process stopped during natural connectivity qualification')
        state=run('nmcli','-g','CONNECTIVITY','general',output=True).strip()
        if state=='full':
            observed+=1
        else:
            observed=0
        if observed>=320:
            return
        time.sleep(1)
    raise ValueError('natural sustained NM FULL not observed')


def run_guest(config_file: Path, external_gateway: str) -> None:
    _is_synthetic_guest()
    # Synthetic endpoint is tied to the disposable veth/dummy fixture. It is
    # not a guessed provider address extracted from arbitrary Xray payloads.
    if external_gateway!='172.31.253.1':
        raise ValueError('unexpected synthetic transport gateway')
    evidence=_underlay_evidence()
    plan=select_underlay(evidence,10)
    _xray_marked_config(config_file,plan.tunnel_mark)
    xray=None
    protected=False
    installed=False
    profile_uuid=str(uuid.uuid4())
    with (ROOT/'nm-l3-xray.stdout').open('w') as output, (ROOT/'nm-l3-xray.stderr').open('w') as errors:
        try:
            _nft_protect(plan.tunnel_mark)
            protected=True
            installed=True  # Cleanup must remain fail-closed even if apply aborts midway.
            _start_underlay(plan)
            xray=subprocess.Popen(('/usr/lib/podlaz/xray','run','-c',str(config_file)),stdout=output,stderr=errors)
            for _ in range(100):
                if xray.poll() is not None:
                    raise ValueError('tracked Xray TUN child exited')
                links=ip_json('-d','link','show','dev','podlaz0') if Path('/sys/class/net/podlaz0').exists() else []
                if links:
                    break
                time.sleep(.2)
            else:
                raise ValueError('tracked Xray TUN did not appear')
            addresses=ip_json('address','show','dev','podlaz0')
            index=links[0]['ifindex']
            validate_xray_tun(index,links,addresses)
            _write_private_intent(external_gateway,index)
            _stage('foreign-collision')
            _qualify_foreign_profile_collision()
            print('nm-l3-foreign-collision-rejected=pass',flush=True)
            _stage('invalid-ip4')
            _qualify_invalid_ip4_apply()
            print('nm-l3-invalid-ip4-rejected=pass',flush=True)
            _stage('nm-activation')
            _activate_ephemeral_profile(10, profile_uuid)
            print('nm-l3-real-dbus-ip4=pass',flush=True)
            _verify_paths(plan.tunnel_mark)
            print('nm-l3-protected-routes-and-https=pass',flush=True)
            _stage('natural-full')
            _natural_full(xray)
            _verify_paths(plan.tunnel_mark)
            print('nm-l3-natural-full=pass',flush=True)
            _stage('uplink-churn')
            _qualify_uplink_churn(xray)
            print('nm-l3-uplink-churn-protected=pass',flush=True)
            _qualify_failure_protection(xray)
            print('nm-l3-failure-protection=pass',flush=True)
            print('nm-l3-ipv6-physical-block=pass',flush=True)
            _stage('cleanup-entry')
        finally:
            # Explicit request first; never treat a similar-looking foreign
            # route or NM object path as cleanup authority.
            _stage('cleanup-active-query')
            if _owned_nm_profile_present(profile_uuid, active=True):
                _stage('cleanup-nm-down')
                run('nmcli','-w','20','connection','down',profile_uuid,timeout=25)
            # The volatile guest profile has a fixed name/UUID created only
            # after collision-free preflight. No name-based foreign deletion.
            _stage('cleanup-owned-query')
            if _owned_nm_profile_present(profile_uuid):
                _stage('cleanup-nm-delete')
                run('nmcli','connection','delete',profile_uuid)
            _stage('cleanup-xray')
            if xray is not None:
                xray.terminate()
                xray.wait(timeout=12)
            _stage('cleanup-underlay-inspect')
            if installed:
                # Fail closed if any foreign route/rule appears: guard exact
                # U composition before deleting its initial owned identity.
                current=ip_json('-4','route','show','table',str(plan.table))
                owned_rules=[r for r in ip_json('-4','rule','show')
                             if r.get('priority') == plan.rule_priority or
                             _numeric_table(r.get('table',r.get('lookup',-1))) == plan.table]
                _stage('cleanup-underlay-verify')
                verify_owned_underlay(plan,current,owned_rules)
                _stage('cleanup-underlay-rule')
                run('ip','-4','rule','del','pref',str(plan.rule_priority),'fwmark',str(plan.tunnel_mark),'lookup',str(plan.table))
                _stage('cleanup-underlay-default')
                run('ip','-4','route','del','default','via',plan.default_route[1],'dev',plan.default_route[2],'table',str(plan.table))
                _stage('cleanup-underlay-link')
                run('ip','-4','route','del',plan.connected_route[0],'dev',plan.connected_route[1],'table',str(plan.table))
            _stage('cleanup-nft')
            if protected:
                # The guest is disposable; never remove barrier while an
                # unknown owner or active VPN might still use the underlay.
                if _owned_nm_profile_present(profile_uuid, active=True):
                    raise ValueError('VPN remains active; barrier retained')
                run('nft','delete','table','inet',TABLE)
            _stage('cleanup-intent')
            if INTENT.exists():
                INTENT.unlink()
                INTENT.parent.rmdir()
    print('nm-l3-exact-guest-cleanup=pass',flush=True)


def main() -> None:
    if len(sys.argv)!=3:
        raise SystemExit('usage: guest-scenario GENERATED_XRAY_CONFIG ACTUAL_GUEST_ENDPOINT')
    try:
        run_guest(Path(sys.argv[1]),sys.argv[2])
    except (OSError,ValueError,RuntimeError,subprocess.SubprocessError,KeyError,TypeError,json.JSONDecodeError):
        print('nm-l3-result=fail',flush=True)
        raise SystemExit(1)
    print('nm-l3-result=pass',flush=True)


if __name__=='__main__':
    main()

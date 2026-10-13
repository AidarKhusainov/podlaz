#!/usr/bin/env python3
"""Throwaway distro-NM selector instrumentation, not an NM behavior change.

Only temporary hosted checkouts/source packages are changed. Every original
selector byte is preserved; removing the marked insertions restores it exactly.
Public evidence contains enums, metrics and fixture roles, never route payloads.
"""
from __future__ import annotations

import os
import re
import sys
from pathlib import Path

UINT = r'[0-9]{1,10}'
HEADER = rf'call_{UINT} family_[46]'
SELECTOR_PATTERNS = tuple(re.compile(pattern) for pattern in (
    rf'nm-decision-selector=phase_(?:managed|vpn) {HEADER} event_begin',
    rf'nm-decision-selector=phase_(?:managed|vpn) {HEADER} ordinal_{UINT} event_(?:skip|consider) role_(?:tun|uplink|other) has_default_[01] metric_{UINT} device_[0-4] best_has_default_[01] best_metric_{UINT} best_state_[0-4]',
    rf'nm-decision-selector=phase_(?:managed|vpn) {HEADER} event_return state_[0-4]',
    rf'nm-decision-selector-update=phase_(?:managed|vpn) first_{UINT} last_{UINT} state_[0-4]',
    rf'nm-decision-selector-cache=phase_(?:managed|vpn) read_{UINT} event_begin first_{UINT} last_{UINT} cached_[0-4] vpn_active_[01]',
    rf'nm-decision-selector-cache=phase_(?:managed|vpn) read_{UINT} family_[46] ordinal_{UINT} role_(?:tun|uplink|other) has_default_[01] metric_{UINT} device_[0-4]',
    rf'nm-decision-selector-cache=phase_(?:managed|vpn) read_{UINT} event_end',
    r'nm-decision-selector-vpn-window=complete',
))
RAW_PATTERNS = (
    re.compile(rf'nm-selector-begin call=({UINT}) family=([46])'),
    re.compile(rf'nm-selector-candidate call=({UINT}) family=([46]) ordinal=({UINT}) event=(skip|consider) role=(tun|uplink|other) has_default=([01]) metric=({UINT}) device=([0-4]) best_has_default=([01]) best_metric=({UINT}) best_state=([0-4])'),
    re.compile(rf'nm-selector-end call=({UINT}) family=([46]) state=([0-4])'),
    re.compile(rf'nm-selector-update first=({UINT}) last=({UINT}) state=([0-4])'),
    re.compile(rf'nm-selector-cache-begin read=({UINT}) first=({UINT}) last=({UINT}) cached=([0-4]) vpn_active=([01])'),
    re.compile(rf'nm-selector-cache-device read=({UINT}) family=([46]) ordinal=({UINT}) role=(tun|uplink|other) has_default=([01]) metric=({UINT}) device=([0-4])'),
    re.compile(rf'nm-selector-cache-end read=({UINT})'),
)


C_HELPER = '''static guint nm_research_serial;
static guint nm_research_update_first;
static guint nm_research_update_last;
static guint nm_research_read_serial;

static void
_podlaz_research_selector_trace(NMManager *self, guint call, guint ordinal,
                               int addr_family, NMDevice *dev,
                               const NMPObject *route, gint64 best_metric,
                               NMConnectivityState best_state, const char *event)
{
    const char *iface = nm_device_get_iface(dev);
    const char *role = nm_streq0(iface, "podlaz0") ? "tun"
                     : nm_streq0(iface, "host0") ? "uplink" : "other";
    _LOGD(LOGD_CORE,
          "nm-selector-candidate call=%u family=%d ordinal=%u event=%s role=%s "
          "has_default=%u metric=%u device=%u best_has_default=%u "
          "best_metric=%u best_state=%u",
          call, addr_family == AF_INET ? 4 : 6, ordinal, event, role,
          (guint) !!route, route ? NMP_OBJECT_CAST_IP_ROUTE(route)->metric : 0u,
          (guint) nm_device_get_connectivity_state(dev, addr_family),
          (guint) (best_metric != G_MAXINT64),
          best_metric != G_MAXINT64 ? (guint) best_metric : 0u,
          (guint) best_state);
}

static void
_podlaz_research_cache_trace(NMManager *self)
{
    NMManagerPrivate *priv = NM_MANAGER_GET_PRIVATE(self);
    NMActiveConnection *ac;
    NMDevice *dev;
    gboolean vpn_active = FALSE;
    guint read_id = ++nm_research_read_serial;
    guint family;

    c_list_for_each_entry (ac, &priv->active_connections_lst_head, active_connections_lst) {
        if (NM_IS_VPN_CONNECTION(ac)
            && nm_active_connection_get_state(ac) == NM_ACTIVE_CONNECTION_STATE_ACTIVATED) {
            vpn_active = TRUE;
            break;
        }
    }
    _LOGD(LOGD_CORE,
          "nm-selector-cache-begin read=%u first=%u last=%u cached=%u vpn_active=%u",
          read_id, nm_research_update_first, nm_research_update_last,
          (guint) priv->connectivity_state, (guint) vpn_active);
    for (family = 4; family <= 6; family += 2) {
        int addr_family = family == 4 ? AF_INET : AF_INET6;
        guint ordinal = 0;
        c_list_for_each_entry (dev, &priv->devices_lst_head, devices_lst) {
            const NMPObject *route;
            const char *iface;
            const char *role;

            if (NM_IS_DEVICE_LOOPBACK(dev))
                continue;
            route = nm_device_get_best_default_route(dev, addr_family);
            iface = nm_device_get_iface(dev);
            role = nm_streq0(iface, "podlaz0") ? "tun"
                 : nm_streq0(iface, "host0") ? "uplink" : "other";
            _LOGD(LOGD_CORE,
                  "nm-selector-cache-device read=%u family=%u ordinal=%u role=%s "
                  "has_default=%u metric=%u device=%u",
                  read_id, family, ++ordinal, role, (guint) !!route,
                  route ? NMP_OBJECT_CAST_IP_ROUTE(route)->metric : 0u,
                  (guint) nm_device_get_connectivity_state(dev, addr_family));
        }
    }
    _LOGD(LOGD_CORE, "nm-selector-cache-end read=%u", read_id);
}

'''


def addition(text: str, language: str = 'c') -> str:
    start, end = ('/* nm-research-begin */', '/* nm-research-end */') if language == 'c' else ('# nm-research-begin', '# nm-research-end')
    return start + '\n' + text + end + '\n'


def strip_instrumentation(text: str) -> str:
    return re.sub(r'(?m)^(?:/\* nm-research-begin \*/|# nm-research-begin)\n.*?^(?:/\* nm-research-end \*/|# nm-research-end)\n', '', text, flags=re.S)


def before(source: str, needle: str, text: str, language: str = 'c') -> str:
    if source.count(needle) != 1:
        raise ValueError('source anchor missing or ambiguous')
    return source.replace(needle, addition(text, language) + needle, 1)


def instrument_manager(source: str) -> str:
    start = 'static NMConnectivityState\n_get_best_connectivity(NMManager *self, int addr_family)\n'
    end = '\nstatic void\ndevice_connectivity_changed('
    if 'nm-research-begin' in source or source.count(start) != 1 or source.count(end) != 1:
        raise ValueError('unsupported or already instrumented selector')
    first, last = source.index(start), source.index(end)
    body = source[first:last]
    body = before(body, '    if (addr_family == AF_UNSPEC) {',
                  '    guint nm_research_call;\n    guint nm_research_ordinal = 0;\n')
    anchor = '    nm_assert_addr_family(addr_family);\n'
    if body.count(anchor) != 1:
        raise ValueError('family boundary missing')
    body = body.replace(anchor, anchor + addition(
        '    nm_research_call = ++nm_research_serial;\n'
        '    _LOGD(LOGD_CORE, "nm-selector-begin call=%u family=%d",\n'
        '          nm_research_call, addr_family == AF_INET ? 4 : 6);\n'), 1)
    body = before(body, '        if (metric > best_metric) {', '        ++nm_research_ordinal;\n')
    # Put the skip record INSIDE the actual branch, not in a parallel model.
    skip_start = body.index('        if (metric > best_metric) {')
    skip_end = body.index('\n        }', skip_start)
    skip = body[skip_start:skip_end]
    skip = before(skip, '            continue;',
                  '            _podlaz_research_selector_trace(self, nm_research_call,\n'
                  '                nm_research_ordinal, addr_family, dev, r, best_metric, best_state, "skip");\n')
    body = body[:skip_start] + skip + body[skip_end:]
    loop_start = body.index('    c_list_for_each_entry (dev, &priv->devices_lst_head, devices_lst) {')
    body = body[:loop_start] + before(body[loop_start:],
        '        if (nm_connectivity_state_cmp(best_state, NM_CONNECTIVITY_FULL) >= 0) {',
        '        _podlaz_research_selector_trace(self, nm_research_call,\n'
        '            nm_research_ordinal, addr_family, dev, r, best_metric, best_state, "consider");\n')
    body = before(body, '    return best_state;\n}',
                  '    _LOGD(LOGD_CORE, "nm-selector-end call=%u family=%d state=%u",\n'
                  '          nm_research_call, addr_family == AF_INET ? 4 : 6, (guint) best_state);\n')
    result = source[:first] + addition(C_HELPER) + body + source[last:]
    # Record an actual global update's family calls. Getter snapshots reference
    # this evaluation; they never call the selector or modify cached connectivity.
    result = before(result, '    best_state = _get_best_connectivity(self, AF_UNSPEC);\n',
                    '    nm_research_update_first = nm_research_serial + 1;\n')
    result = before(result, '    if (best_state == priv->connectivity_state)\n',
                    '    nm_research_update_last = nm_research_serial;\n'
                    '    _LOGD(LOGD_CORE, "nm-selector-update first=%u last=%u state=%u",\n'
                    '          nm_research_update_first, nm_research_update_last, (guint) best_state);\n')
    if result.count('    case PROP_CONNECTIVITY:\n        g_value_set_uint(value, priv->connectivity_state);\n') != 1:
        raise ValueError('cached connectivity getter missing or ambiguous')
    result = before(result, '        g_value_set_uint(value, priv->connectivity_state);\n',
                    '        _podlaz_research_cache_trace(self);\n')
    if strip_instrumentation(result) != source:
        raise ValueError('instrumentation modified original selector')
    return result


def prepare_harness(source: str) -> str:
    if 'nm-research-begin' in source:
        raise ValueError('fixture already prepared')
    source = before(source, '  sudo -n rm -f "${policy_rc}"\n',
                  '  bash "${SCRIPT_DIR}/nm_route_selection_guest.sh" "${GUEST_ROOT}" "${PRIVATE_ROOT}"\n', 'script')
    # End the diagnostic VPN window while the real plugin is still active.
    # The ordinary teardown and its later cumulative trace remain unchanged.
    return before(source, '    guest_exec nmcli -w 20 connection down podlaz-guest-research >/dev/null\n',
                  '    guest_exec python3 /workspace/scripts/e2e/nm_registration_evidence.py observe\n'
                  '    guest_exec python3 /workspace/scripts/e2e/nm_registration_evidence.py compare "${GUEST_PRIVATE}/nm-vpn-before.json"\n'
                  "    printf 'nm-decision-selector-vpn-window=complete\\n'\n", 'script')


def prepare_trace(source: str) -> str:
    if 'nm-research-begin' in source:
        raise ValueError('trace already prepared')
    source = before(source, '\ndef command(',
                    'from nm_route_selection_probe import SELECTOR_PATTERNS, selector_records\n'
                    'PUBLIC_PATTERNS += SELECTOR_PATTERNS\n', 'script')
    # Add only closed-schema selector records; existing concheck and scenario
    # validators remain unchanged and still fail on missing evidence.
    return before(source, '    records = concheck_records(messages, phase)\n',
                  '    for selector_record in selector_records(messages, phase):\n'
                  '        print(selector_record)\n', 'script')


def public_selector_record(line: str) -> bool:
    return (any(pattern.fullmatch(line) for pattern in SELECTOR_PATTERNS)
            and all(int(number) <= 0xffffffff for number in re.findall(r'_(\d+)\b', line)))


def selector_records(messages: list[str], phase: str) -> list[str]:
    if phase not in ('managed', 'vpn'):
        raise ValueError('unknown selector phase')
    result = []
    for message in messages:
        if 'nm-selector-' not in message:
            continue
        raw = message[message.index('nm-selector-'):]
        kind = 'selector'
        match = RAW_PATTERNS[0].fullmatch(raw)
        if match:
            line = f'call_{match[1]} family_{match[2]} event_begin'
        elif (match := RAW_PATTERNS[1].fullmatch(raw)):
            line = (f'call_{match[1]} family_{match[2]} ordinal_{match[3]} event_{match[4]} role_{match[5]}'
                    f' has_default_{match[6]} metric_{match[7]} device_{match[8]}'
                    f' best_has_default_{match[9]} best_metric_{match[10]} best_state_{match[11]}')
        elif (match := RAW_PATTERNS[2].fullmatch(raw)):
            line = f'call_{match[1]} family_{match[2]} event_return state_{match[3]}'
        elif (match := RAW_PATTERNS[3].fullmatch(raw)):
            kind = 'selector-update'
            line = f'first_{match[1]} last_{match[2]} state_{match[3]}'
        elif (match := RAW_PATTERNS[4].fullmatch(raw)):
            kind = 'selector-cache'
            line = (f'read_{match[1]} event_begin first_{match[2]} last_{match[3]}'
                    f' cached_{match[4]} vpn_active_{match[5]}')
        elif (match := RAW_PATTERNS[5].fullmatch(raw)):
            kind = 'selector-cache'
            line = (f'read_{match[1]} family_{match[2]} ordinal_{match[3]} role_{match[4]}'
                    f' has_default_{match[5]} metric_{match[6]} device_{match[7]}')
        elif (match := RAW_PATTERNS[6].fullmatch(raw)):
            kind = 'selector-cache'
            line = f'read_{match[1]} event_end'
        else:
            raise ValueError('malformed private selector event')
        record = f'nm-decision-{kind}=phase_{phase} ' + line
        if not public_selector_record(record):
            raise ValueError('selector event exceeds public bounds')
        result.append(record)
    return result


def _field(line: str, name: str) -> str:
    match = re.search(r'(?:^| )' + re.escape(name) + r'_([^ ]+)(?: |$)', line)
    if match is None:
        raise ValueError('required selector field missing')
    return match[1]


def _complete_call(rows: list[str], full_tun: bool = True) -> bool:
    if len(rows) < 3 or not rows[0].endswith('event_begin') or ' event_return state_' not in rows[-1]:
        return False
    middle = rows[1:-1]
    if any(' ordinal_' not in row for row in middle):
        return False
    if [int(_field(row, 'ordinal')) for row in middle] != list(range(1, len(middle) + 1)):
        return False
    return (not full_tun or
            (any(' role_tun ' in row and ' device_4 ' in row for row in middle)
             and any(' role_uplink ' in row for row in middle)))


def _retained_vpn_evidence(unique: list[str]) -> bool:
    # Validate the latest complete read, not an earlier convenient cache value.
    # No routing algorithm is reimplemented: the actual updater records its
    # result, and the getter reports cached state and current inputs separately.
    boundary = 'nm-decision-selector-vpn-window=complete'
    if boundary not in unique:
        return False
    unique = unique[:unique.index(boundary)]
    snapshots: dict[int, list[str]] = {}
    calls: dict[tuple[int, int], list[str]] = {}
    updates: dict[tuple[int, int], set[int]] = {}
    for line in unique:
        if line.startswith('nm-decision-selector-cache=phase_vpn '):
            snapshots.setdefault(int(_field(line, 'read')), []).append(line)
        elif line.startswith('nm-decision-selector='):
            key = (int(_field(line, 'call')), int(_field(line, 'family')))
            row = line.split(' ', 1)[1]
            if row not in calls.setdefault(key, []):
                calls[key].append(row)
        elif line.startswith('nm-decision-selector-update='):
            key = (int(_field(line, 'first')), int(_field(line, 'last')))
            updates.setdefault(key, set()).add(int(_field(line, 'state')))
    if not snapshots:
        return False
    rows = snapshots[max(snapshots)]
    if len(rows) < 6 or ' event_begin ' not in rows[0] or not rows[-1].endswith('event_end'):
        return False
    first, last = int(_field(rows[0], 'first')), int(_field(rows[0], 'last'))
    if first <= 0 or last - first not in (0, 1) or _field(rows[0], 'vpn_active') != '1':
        return False
    if updates.get((first, last)) != {int(_field(rows[0], 'cached'))}:
        return False
    if not _complete_call(calls.get((first, 4), [])):
        return False
    if last != first and not _complete_call(calls.get((last, 6), []), full_tun=False):
        return False
    middle = rows[1:-1]
    if any(' family_' not in row for row in middle):
        return False
    for family in (4, 6):
        devices = [row for row in middle if _field(row, 'family') == str(family)]
        if [int(_field(row, 'ordinal')) for row in devices] != list(range(1, len(devices) + 1)):
            return False
        if not all(any(_field(row, 'role') == role for row in devices) for role in ('tun', 'uplink')):
            return False
        if family == 4 and not any(_field(row, 'role') == 'tun' and _field(row, 'device') == '4' for row in devices):
            return False
    return True


def validate_selector_lines(lines: list[str]) -> None:
    # Calls repeat in cumulative journal windows. Deduplicate exact rows only;
    # incomplete calls never establish selection evidence.
    unique = list(dict.fromkeys(lines))
    if any(line.startswith('nm-decision-selector') and not public_selector_record(line) for line in unique):
        raise ValueError('invalid selector artifact')
    for phase in ('managed', 'vpn'):
        prefix = f'nm-decision-selector=phase_{phase} '
        calls: dict[tuple[str, str], list[str]] = {}
        for line in unique:
            if line.startswith(prefix):
                calls.setdefault((_field(line, 'call'), _field(line, 'family')), []).append(line)
        complete = any(family == '4' and _complete_call(rows) for (_, family), rows in calls.items())
        if not complete and not (phase == 'vpn' and _retained_vpn_evidence(unique)):
            raise ValueError('complete actual selection or proven retained VPN result missing')
        probe = f'nm-decision-concheck=phase_{phase} role_tun family_4 result_full '
        if not any(line.startswith(probe) for line in unique):
            raise ValueError('real FULL TUN probe missing')


def main() -> None:
    if len(sys.argv) != 3:
        raise ValueError('invalid probe invocation')
    mode, path = sys.argv[1], Path(sys.argv[2])
    if mode == 'instrument':
        original = path.read_text(encoding='utf-8')
        path.write_text(instrument_manager(original), encoding='utf-8')
    elif mode == 'fixture':
        if os.environ.get('GITHUB_ACTIONS') != 'true':
            raise ValueError('throwaway hosted checkout required')
        harness = path / 'scripts/e2e/hosted-synthetic-tun.sh'
        trace = path / 'scripts/e2e/nm_connectivity_trace.py'
        old_harness, old_trace = harness.read_text(), trace.read_text()
        new_harness, new_trace = prepare_harness(old_harness), prepare_trace(old_trace)
        if strip_instrumentation(new_harness) != old_harness or strip_instrumentation(new_trace) != old_trace:
            raise ValueError('fixture changes exceed diagnostic insertions')
        harness.write_text(new_harness)
        trace.write_text(new_trace)
    elif mode == 'validate':
        validate_selector_lines(path.read_text().splitlines())
        print('nm-selector-runtime-evidence=complete')
    else:
        raise ValueError('unknown probe mode')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError):
        print('nm-selector-probe=failed', file=sys.stderr)
        sys.exit(1)

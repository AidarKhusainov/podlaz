"""Compile and exercise diagnostic hooks without changing selector cadence."""
import subprocess
import tempfile
import unittest
from pathlib import Path

import nm_route_selection_probe as probe
from nm_route_selection_probe_test import SOURCE

STUBS = r'''
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <assert.h>
typedef unsigned guint;
typedef long long gint64;
typedef int gboolean;
typedef unsigned NMConnectivityState;
typedef struct { unsigned metric; } NMPObject;
typedef struct { void *next; } Link;
typedef struct { void *first; } Head;
typedef struct { const char *iface; NMPObject *r4; unsigned v4, v6; Link devices_lst; } NMDevice;
typedef struct { int vpn; unsigned state; Link active_connections_lst; } NMActiveConnection;
typedef struct { unsigned connectivity_state; Head devices_lst_head, active_connections_lst_head; } NMManagerPrivate;
typedef struct { NMManagerPrivate data; } NMManager;
#define NM_MANAGER_GET_PRIVATE(self) (&(self)->data)
#define TRUE 1
#define FALSE 0
#define AF_UNSPEC 0
#define AF_INET 2
#define AF_INET6 10
#define NM_CONNECTIVITY_UNKNOWN 0
#define NM_CONNECTIVITY_FULL 4
#define G_MAXINT64 INT64_MAX
#define PROP_CONNECTIVITY 1
#define LOGD_CORE 0
#define _LOGD(domain, ...) do { printf(__VA_ARGS__); puts(""); } while (0)
#define c_list_for_each_entry(item, head, member) for (item = (head)->first; item; item = item->member.next)
#define NM_IS_DEVICE_LOOPBACK(dev) (!strcmp((dev)->iface, "lo"))
#define NMP_OBJECT_CAST_IP_ROUTE(r) (r)
#define nm_streq0(a,b) (!strcmp(a,b))
#define NM_IS_VPN_CONNECTION(ac) ((ac)->vpn)
#define NM_ACTIVE_CONNECTION_STATE_ACTIVATED 2
#define NM_MAX_WITH_CMP(cmp,a,b) ((a) > (b) ? (a) : (b))
static int nm_connectivity_state_cmp(unsigned a, unsigned b) { return (a>b)-(a<b); }
static const char *nm_device_get_iface(NMDevice *dev) { return dev->iface; }
static unsigned nm_device_get_connectivity_state(NMDevice *dev, int af) { return af==AF_INET?dev->v4:dev->v6; }
static NMPObject *nm_device_get_best_default_route(NMDevice *dev, int af) { return af==AF_INET?dev->r4:NULL; }
static unsigned nm_active_connection_get_state(NMActiveConnection *ac) { return ac->state; }
static void nm_assert_addr_family(int af) { assert(af==AF_INET || af==AF_INET6); }
static void g_value_set_uint(unsigned *value, unsigned state) { *value=state; }
'''
MAIN = r'''
int main(void) {
    NMPObject route = {100};
    NMDevice tun = {.iface="podlaz0", .v4=4, .v6=3};
    NMDevice uplink = {.iface="host0", .r4=&route, .v4=3, .v6=1, .devices_lst={&tun}};
    NMActiveConnection vpn = {.vpn=1, .state=NM_ACTIVE_CONNECTION_STATE_ACTIVATED};
    NMManager manager = {.data={.devices_lst_head={&uplink}, .active_connections_lst_head={&vpn}}};
    unsigned value = 0;
    update_connectivity_value(&manager);
    guint actual_calls = nm_research_serial;
    get_property(&manager, PROP_CONNECTIVITY, &value);
    assert(value==3 && manager.data.connectivity_state==3);
    assert(nm_research_serial==actual_calls);
    route.metric=20100;
    get_property(&manager, PROP_CONNECTIVITY, &value);
    assert(nm_research_serial==actual_calls);
    assert(value==3 && manager.data.connectivity_state==3);
    return 0;
}
'''


class CacheCContract(unittest.TestCase):
    def test_cache_read_logs_current_inputs_but_does_not_run_selector(self):
        source = SOURCE.replace('preamble\n', STUBS)
        source = source.replace(
            '_get_best_connectivity(NMManager *self, int addr_family)\n{',
            '_get_best_connectivity(NMManager *self, int addr_family)\n{\n    NMManagerPrivate *priv = NM_MANAGER_GET_PRIVATE(self);',
        )
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path/'probe.c').write_text(probe.instrument_manager(source) + MAIN)
            compiled = subprocess.run(
                ['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror', '-Wno-unused-parameter',
                 '-Wno-unused-function', str(path/'probe.c'), '-o', str(path/'probe')],
                capture_output=True, text=True, timeout=20, check=False,
            )
            self.assertEqual(compiled.returncode, 0, compiled.stderr)
            executed = subprocess.run([str(path/'probe')], capture_output=True, text=True, timeout=5, check=True)
        output = executed.stdout
        self.assertEqual(output.count('nm-selector-begin '), 1)
        self.assertEqual(output.count('nm-selector-cache-begin '), 2)
        self.assertIn('cached=3 vpn_active=1', output)
        self.assertIn('ordinal=1 role=uplink has_default=1 metric=20100 device=3', output)
        self.assertIn('ordinal=2 role=tun has_default=0 metric=0 device=4', output)
        records = probe.selector_records(output.splitlines(), 'vpn')
        self.assertTrue(all(probe.public_selector_record(row) for row in records))


if __name__ == '__main__':
    unittest.main()

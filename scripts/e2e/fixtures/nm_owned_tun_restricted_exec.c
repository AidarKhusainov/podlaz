#define _GNU_SOURCE
/*
 * Guest-only capability handoff for a pinned, research Xray binary.
 * Not a production launcher and not an FD identity/revocation guarantee.
 */
#include <grp.h>
#include <linux/capability.h>
#include <pwd.h>
#include <stdio.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <unistd.h>

static void die(const char *stage) {
    fprintf(stderr, "research Xray privilege handoff failed: %s\n", stage);
    _exit(125);
}

int main(int argc, char **argv) {
    const char *binary = "/run/podlaz-synthetic-xray/nm-owned-external-xray";
    if (argc < 3 || strcmp(argv[1], binary) != 0 || getuid() != 0 || geteuid() != 0)
        die("arguments-or-identity");

    struct stat st;
    if (lstat(binary, &st) != 0 || !S_ISREG(st.st_mode) ||
        st.st_uid != 0 || (st.st_mode & 022) != 0 ||
        (st.st_mode & (S_ISUID | S_ISGID)) != 0)
        die("untrusted-executable");

    struct passwd *user = getpwnam("e2e");
    if (!user || user->pw_uid == 0 || user->pw_gid == 0)
        die("unqualified-guest-user");
    uid_t uid = user->pw_uid;
    gid_t gid = user->pw_gid;

    FILE *last_file = fopen("/proc/sys/kernel/cap_last_cap", "r");
    unsigned int last = 0;
    if (!last_file || fscanf(last_file, "%u", &last) != 1)
        die("capability-inventory");
    if (fclose(last_file) != 0 || last < CAP_NET_RAW || last >= 64 ||
        prctl(PR_CAPBSET_READ, CAP_NET_RAW, 0, 0, 0) != 1)
        die("unsupported-capability-boundary");

    if (prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0) != 0)
        die("clear-ambient");
    for (unsigned int cap = 0; cap <= last; ++cap) {
        if (cap != CAP_NET_RAW && prctl(PR_CAPBSET_DROP, cap, 0, 0, 0) != 0)
            die("drop-bounding");
    }
    if (prctl(PR_SET_KEEPCAPS, 1, 0, 0, 0) != 0 ||
        setgroups(0, NULL) != 0 ||
        setresgid(gid, gid, gid) != 0 ||
        setresuid(uid, uid, uid) != 0)
        die("drop-root-identity");

    struct __user_cap_header_struct header = {
        .version = _LINUX_CAPABILITY_VERSION_3, .pid = 0
    };
    struct __user_cap_data_struct data[2] = {{0}, {0}};
    data[CAP_NET_RAW / 32].permitted = 1U << (CAP_NET_RAW % 32);
    data[CAP_NET_RAW / 32].effective = 1U << (CAP_NET_RAW % 32);
    data[CAP_NET_RAW / 32].inheritable = 1U << (CAP_NET_RAW % 32);
    if (syscall(SYS_capset, &header, data) != 0 ||
        prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE, CAP_NET_RAW, 0, 0) != 0 ||
        prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0)
        die("restrict-credentials");

    /* exec preserves PID, inherited TUN FD, and exactly CAP_NET_RAW. */
    execv(binary, &argv[1]);
    die("exec");
}

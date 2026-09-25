package shell

/*
#include <libproc.h>
#include <string.h>
#include <sys/param.h>
#include <sys/proc_info.h>

// shellterCwd copies the working directory of pid into buf, returning 0 on
// success. It is the kernel's answer, the same one `lsof -d cwd` gives.
static int shellterCwd(int pid, char *buf, int len) {
	struct proc_vnodepathinfo info;
	int n = proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, &info, sizeof info);
	if (n != (int)sizeof info) {
		return -1;
	}
	strlcpy(buf, info.pvi_cdir.vip_path, len);
	return 0;
}
*/
import "C"

import "unsafe"

// Dir is the shell's current working directory, or "" if it cannot be read -
// the shell has gone, or belongs to someone else.
//
// It asks the kernel rather than the shell, so it works for every shell with
// no setup at all. A shell that reports its directory through OSC 7 says the
// same thing; that report is still worth having, because it is the only one
// that can describe a directory on the far side of ssh.
func (s *Session) Dir() string {
	pid := s.Pid()
	if pid <= 0 {
		return ""
	}
	var buf [C.MAXPATHLEN]C.char
	if C.shellterCwd(C.int(pid), &buf[0], C.int(len(buf))) != 0 {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
}

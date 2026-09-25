package shell

import (
	"os"
	"os/exec"
	"strings"
)

// environment is the shell's environment: ours, with the variables a terminal
// is responsible for set to what Shellter is rather than to whatever we were
// started with.
//
// "Ours" is a different thing depending on how we were started. From another
// terminal it is that terminal's, TERM and all. From the Dock it is launchd's,
// which has no locale at all - and a shell with no locale runs in C, where an
// é is two bytes of garbage. Terminal.app sets a locale for the same reason.
//
// Variables are replaced, never appended twice: with two TERMs in the list,
// which one a program sees depends on how it reads its environment.
func environment(base []string, locale func() string) []string {
	env := set(base, "TERM", "xterm-256color")
	// TERM cannot say "24-bit colour" - xterm-256color is the most it names -
	// so programs that can use it (bat, delta, vim themes) look here instead.
	env = set(env, "COLORTERM", "truecolor")
	if !hasAny(env, "LC_ALL", "LC_CTYPE", "LANG") {
		env = set(env, "LANG", locale())
	}
	return env
}

// set returns env with key=value, replacing key's first entry if it has one
// and dropping any later ones.
func set(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	done := false
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == key {
			if !done {
				out = append(out, key+"="+value)
				done = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !done {
		out = append(out, key+"="+value)
	}
	return out
}

// hasAny reports whether env gives any of keys a non-empty value.
func hasAny(env []string, keys ...string) bool {
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		for _, key := range keys {
			if k == key && v != "" {
				return true
			}
		}
	}
	return false
}

// systemLocale is the language the user picked in System Settings, as a UTF-8
// locale name: "en_IN" becomes "en_IN.UTF-8". A language macOS has no locale
// files for falls back to US English, which is at least UTF-8 - the part that
// decides whether text survives.
func systemLocale() string {
	const fallback = "en_US.UTF-8"
	out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output()
	if err != nil {
		return fallback
	}
	// "en_IN@rg=usxxxx" carries a region override after the @; the locale is
	// what comes before it.
	name, _, _ := strings.Cut(strings.TrimSpace(string(out)), "@")
	if name == "" {
		return fallback
	}
	name += ".UTF-8"
	if _, err := os.Stat("/usr/share/locale/" + name); err != nil {
		return fallback
	}
	return name
}

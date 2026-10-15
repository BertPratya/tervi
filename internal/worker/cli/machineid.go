package cli

import "fmt"

func confirmMachineID(env Env, flow Flow, yes bool) bool {
	if yes || flow == nil || flow.HasMachineID() {
		return true
	}
	fmt.Fprintln(env.Stdout, "! tervi can't read this computer's machine ID (/etc/machine-id).")
	fmt.Fprintln(env.Stdout, "  Without it, the server won't recognize this computer if you pair it")
	fmt.Fprintln(env.Stdout, "  again later: it will come back as a new computer, without its history.")
	fmt.Fprintln(env.Stdout, "  To fix this, create a machine ID first: sudo systemd-machine-id-setup")
	fmt.Fprint(env.Stdout, "Pair without it? [y/N] ")
	if env.Stdin == nil {
		fmt.Fprintln(env.Stdout, "Nothing was changed.")
		return false
	}
	answer, complete := readAnswer(env.Stdin)
	if complete && isYes(answer) {
		return true
	}
	fmt.Fprintln(env.Stdout, "Nothing was changed.")
	return false
}

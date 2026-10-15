package cli

import (
	"fmt"
	"io"
	"strings"
)

func confirmMachineID(env Env, flow Flow, yes bool) bool {
	if yes || flow == nil || flow.HasMachineID() {
		return true
	}
	fmt.Fprintln(env.Stdout, "! tervi can't read this computer's machine ID (/etc/machine-id).")
	fmt.Fprintln(env.Stdout, "  Without it, the server won't recognize this computer if you pair it")
	fmt.Fprintln(env.Stdout, "  again later: it will come back as a new computer, without its history.")
	fmt.Fprintln(env.Stdout, "  To fix this, create a machine ID first: sudo systemd-machine-id-setup")
	fmt.Fprintln(env.Stdout, "Pair without it? [y/N]")
	if env.Stdin == nil {
		fmt.Fprintln(env.Stdout, "Nothing was changed.")
		return false
	}
	answer, complete := readMachineIDAnswer(env.Stdin)
	if complete {
		answer = strings.TrimSpace(answer)
		if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
			return true
		}
	}
	fmt.Fprintln(env.Stdout, "Nothing was changed.")
	return false
}

func readMachineIDAnswer(input io.Reader) (string, bool) {
	var answer strings.Builder
	for {
		var char [1]byte
		n, err := io.ReadFull(input, char[:])
		if err != nil {
			return "", false
		}
		if n > 0 {
			if char[0] == '\n' {
				return answer.String(), true
			}
			answer.WriteByte(char[0])
		}
	}
}

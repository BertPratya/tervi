package cli

import (
	"io"
	"strings"
)

func readAnswer(input io.Reader) (string, bool) {
	if input == nil {
		return "", false
	}

	var answer strings.Builder
	for {
		var char [1]byte
		n, err := input.Read(char[:])
		if n > 0 {
			if char[0] == '\n' {
				return answer.String(), true
			}
			answer.WriteByte(char[0])
		}
		if err != nil {
			if answer.Len() > 0 {
				return answer.String(), true
			}
			return "", false
		}
	}
}

func isYes(answer string) bool {
	answer = strings.TrimSpace(answer)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

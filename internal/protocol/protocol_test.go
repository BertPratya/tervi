package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessagesUseAsyncAPIFields(t *testing.T) {
	hello, err := json.Marshal(Hello{Type: TypeHello, CheckIntervalSeconds: 10, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(hello), `{"type":"hello","check_interval_seconds":10,"timeout_seconds":30}`; got != want {
		t.Fatalf("hello = %s, want %s", got, want)
	}
	for _, message := range []struct {
		value any
		want  string
	}{
		{Ping{Type: TypePing}, `{"type":"ping"}`},
		{Pong{Type: TypePong}, `{"type":"pong"}`},
	} {
		got, err := json.Marshal(message.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != message.want {
			t.Errorf("message = %s, want %s", got, message.want)
		}
	}
	if got := string(PingMessage()); got != `{"type":"ping"}` {
		t.Errorf("PingMessage() = %s, want {\"type\":\"ping\"}", got)
	}
	if got := string(PongMessage()); got != `{"type":"pong"}` {
		t.Errorf("PongMessage() = %s, want {\"type\":\"pong\"}", got)
	}
}

func TestReadType(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
		ok   bool
	}{
		{name: "known", data: `{"type":"ping"}`, want: TypePing, ok: true},
		{name: "unknown", data: `{"type":"future"}`, want: "future", ok: true},
		{name: "not object", data: `[]`},
		{name: "not json", data: `{`},
		{name: "missing type", data: `{}`},
		{name: "non string type", data: `{"type":7}`},
		{name: "null type", data: `{"type":null}`},
		{name: "object type", data: `{"type":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ReadType([]byte(test.data))
			if got != test.want || ok != test.ok {
				t.Fatalf("ReadType() = (%q, %t), want (%q, %t)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestProtocolConstants(t *testing.T) {
	if MaxMessageSize != 4096 {
		t.Errorf("MaxMessageSize = %d, want 4096", MaxMessageSize)
	}
	if DefaultHelloTimeout != 30*time.Second {
		t.Errorf("DefaultHelloTimeout = %s, want 30s", DefaultHelloTimeout)
	}
	want := map[uint16]int{
		1000: CloseNormal,
		1001: CloseServerStopping,
		1008: CloseInvalidMessage,
		1009: CloseTooLarge,
		4000: CloseReplaced,
		4001: CloseTimeout,
	}
	for code, got := range want {
		if got != int(code) {
			t.Errorf("close code = %d, want %d", got, code)
		}
	}
}

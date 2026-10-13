package control

import (
	"encoding/json"
	"net"
	"net/http"
	"syscall"
)

func handler(engine Engine) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, statusAnswer(engine.Status()))
	})
	mux.HandleFunc("POST /connect", func(w http.ResponseWriter, request *http.Request) {
		writeJSON(w, connectAnswer(engine.Connect(request.Context())))
	})
	return mux
}

func writeJSON(w http.ResponseWriter, answer any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

type sameUserListener struct {
	net.Listener
	uid uint32
}

func (listener *sameUserListener) Accept() (net.Conn, error) {
	for {
		conn, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			continue
		}
		uid, err := socketPeerUID(unixConn)
		if err == nil && peerAllowed(uid, listener.uid) {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func socketPeerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *syscall.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, controlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if controlErr != nil {
		return 0, controlErr
	}
	if credentials == nil {
		return 0, syscall.EINVAL
	}
	return credentials.Uid, nil
}

func peerAllowed(peerUID, daemonUID uint32) bool { return peerUID == daemonUID }

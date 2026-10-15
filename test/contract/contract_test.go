package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bertpratya/tervi/api"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/connection"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/coder/websocket"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

var bodyDecoderOnce sync.Once

func registerBodyDecoders() {
	bodyDecoderOnce.Do(func() {
		openapi3.SchemaErrorDetailsDisabled = true
		openapi3filter.RegisterBodyDecoder("text/html", openapi3filter.PlainBodyDecoder)
	})
}

func TestServerMatchesContract(t *testing.T) {
	registerBodyDecoders()
	pool := dbtest.New(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on loopback: %v", err)
	}
	address := listener.Addr().String()
	baseURL := "http://" + address
	handler := server.New(server.Config{PublicURL: baseURL}, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), connection.NewList())
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener = listener
	ts.Start()
	t.Cleanup(ts.Close)

	doc := loadContract(t)
	doc.Servers = openapi3.Servers{&openapi3.Server{URL: ts.URL}}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("build contract router: %v", err)
	}
	client := &contractClient{
		server: ts,
		router: router,
		seen:   make(map[string]map[int]bool),
	}

	startA := client.startPairing(t, "worker-a")
	pollingA := startA.PollingKey
	approvalPathA := approvalPath(t, startA.ApprovalURL)
	approvalHeadersA := bearerHeaders(approvalKey(t, startA.ApprovalURL))
	response := client.call(t, http.MethodGet, "/api/v1/pairings/current", "", bearerHeaders(pollingA), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "waiting_for_approval")
	response = client.call(t, http.MethodGet, "/api/v1/approvals/current", "", approvalHeadersA, callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "waiting_for_approval")
	response = client.call(t, http.MethodGet, approvalPathA, "", nil, callOptions{})
	expectStatus(t, response, http.StatusOK)
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approvalHeadersA, callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "waiting_for_code")
	codeA := jsonField(t, response, "pairing_code")
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approvalHeadersA, callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "waiting_for_code")
	expectJSONField(t, response, "already_decided", true)
	response = client.call(t, http.MethodGet, "/api/v1/pairings/current", "", bearerHeaders(pollingA), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "waiting_for_code")
	expectJSONFieldPresent(t, response, "tries_left")
	response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", jsonBody(map[string]string{"code": wrongCode(codeA)}), bearerHeaders(pollingA), callOptions{headers: jsonHeaders()})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "result", "wrong_code")
	response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", jsonBody(map[string]string{"code": codeA}), bearerHeaders(pollingA), callOptions{headers: jsonHeaders()})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "result", "accepted")
	credentialA := jsonField(t, response, "credential")
	response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", jsonBody(map[string]string{"code": codeA}), bearerHeaders(pollingA), callOptions{headers: jsonHeaders()})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "result", "not_waiting_for_code")
	response = client.call(t, http.MethodPost, "/api/v1/machines/current/acknowledgment", "", bearerHeaders(credentialA), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "result", "ok")
	client.openConnection(t, credentialA)
	response = client.call(t, http.MethodGet, "/api/v1/machines/current/connection", "", bearerHeaders(credentialA), callOptions{})
	expectStatus(t, response, http.StatusBadRequest)
	expectJSONField(t, response, "error", "invalid_input")
	response = client.call(t, http.MethodGet, "/api/v1/machines/current/connection", "", bearerHeaders("unknown-connection-credential"), callOptions{})
	expectStatus(t, response, http.StatusUnauthorized)
	expectJSONField(t, response, "error", "unknown_credential")
	originHeaders := make(http.Header)
	originHeaders.Set("Origin", "https://browser.example")
	response = client.call(t, http.MethodGet, "/api/v1/machines/current/connection", "", bearerHeaders(credentialA), callOptions{headers: originHeaders})
	expectStatus(t, response, http.StatusForbidden)
	expectJSONField(t, response, "error", "forbidden_origin")
	response = client.call(t, http.MethodGet, "/api/v1/pairings/current", "", bearerHeaders(pollingA), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "paired")
	response = client.call(t, http.MethodGet, "/api/v1/approvals/current", "", approvalHeadersA, callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "paired")
	expectJSONFieldPresent(t, response, "display_name")
	response = client.call(t, http.MethodGet, "/api/v1/machines", "", nil, callOptions{})
	expectStatus(t, response, http.StatusOK)
	var listedMachines struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.Unmarshal(response.body, &listedMachines); err != nil {
		t.Fatalf("decode listMachines response: %v", err)
	}
	if len(listedMachines.Machines) == 0 {
		t.Fatal("listMachines returned no active machine after pairing")
	}
	response = client.call(t, http.MethodGet, "/machines", "", nil, callOptions{})
	expectStatus(t, response, http.StatusOK)

	startB := client.startPairing(t, "worker-b")
	approvalHeadersB := bearerHeaders(approvalKey(t, startB.ApprovalURL))
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approvalHeadersB, callOptions{})
	expectStatus(t, response, http.StatusOK)
	codeB := jsonField(t, response, "pairing_code")
	for attempt := 0; attempt < 5; attempt++ {
		response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", jsonBody(map[string]string{"code": wrongCode(codeB)}), bearerHeaders(startB.PollingKey), callOptions{headers: jsonHeaders()})
		expectStatus(t, response, http.StatusOK)
		if attempt == 4 {
			expectJSONField(t, response, "result", "failed")
		} else {
			expectJSONField(t, response, "result", "wrong_code")
		}
	}
	response = client.call(t, http.MethodGet, "/api/v1/pairings/current", "", bearerHeaders(startB.PollingKey), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "failed")
	expectJSONField(t, response, "failure_reason", "wrong_codes")

	startC := client.startPairing(t, "worker-c")
	approvalHeadersC := bearerHeaders(approvalKey(t, startC.ApprovalURL))
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/accept", "", approvalHeadersC, callOptions{})
	expectStatus(t, response, http.StatusOK)
	codeC := jsonField(t, response, "pairing_code")
	response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", jsonBody(map[string]string{"code": codeC}), bearerHeaders(startC.PollingKey), callOptions{headers: jsonHeaders()})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "result", "accepted")
	credentialC := jsonField(t, response, "credential")
	response = client.call(t, http.MethodPost, "/api/v1/machines/current/save-failure", "", bearerHeaders(credentialC), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "result", "ok")

	startD := client.startPairing(t, "worker-d")
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/reject", "", bearerHeaders(approvalKey(t, startD.ApprovalURL)), callOptions{})
	expectStatus(t, response, http.StatusOK)
	expectJSONField(t, response, "status", "rejected")

	// Error cases, including the deliberately invalid request bodies.
	response = client.call(t, http.MethodGet, "/health", "", nil, callOptions{})
	expectStatus(t, response, http.StatusOK)
	response = client.call(t, http.MethodPost, "/api/v1/pairings", `{"hostname":5}`, jsonHeaders(), callOptions{skipRequestValidation: true})
	expectStatus(t, response, http.StatusBadRequest)
	expectJSONField(t, response, "error", "invalid_input")
	response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", `{}`, bearerHeaders(pollingA), callOptions{headers: jsonHeaders(), skipRequestValidation: true})
	expectStatus(t, response, http.StatusBadRequest)
	expectJSONField(t, response, "error", "invalid_input")
	response = client.call(t, http.MethodGet, "/api/v1/pairings/current", "", nil, callOptions{})
	expectStatus(t, response, http.StatusUnauthorized)
	expectJSONField(t, response, "error", "unknown_key")
	response = client.call(t, http.MethodPost, "/api/v1/pairings/current/code", jsonBody(map[string]string{"code": "1234-5678"}), nil, callOptions{headers: jsonHeaders()})
	expectStatus(t, response, http.StatusUnauthorized)
	expectJSONField(t, response, "error", "unknown_key")
	unknownCredential := bearerHeaders("unknown-credential")
	response = client.call(t, http.MethodPost, "/api/v1/machines/current/acknowledgment", "", unknownCredential, callOptions{})
	expectStatus(t, response, http.StatusUnauthorized)
	expectJSONField(t, response, "error", "unknown_credential")
	response = client.call(t, http.MethodPost, "/api/v1/machines/current/save-failure", "", unknownCredential, callOptions{})
	expectStatus(t, response, http.StatusUnauthorized)
	expectJSONField(t, response, "error", "unknown_credential")
	unknownApproval := bearerHeaders("unknown-approval-key")
	response = client.call(t, http.MethodGet, "/api/v1/approvals/current", "", unknownApproval, callOptions{})
	expectStatus(t, response, http.StatusNotFound)
	expectJSONField(t, response, "error", "invalid_link")
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/accept", "", unknownApproval, callOptions{})
	expectStatus(t, response, http.StatusNotFound)
	expectJSONField(t, response, "error", "invalid_link")
	response = client.call(t, http.MethodPost, "/api/v1/approvals/current/reject", "", unknownApproval, callOptions{})
	expectStatus(t, response, http.StatusNotFound)
	expectJSONField(t, response, "error", "invalid_link")

	if _, err := pool.Exec(context.Background(), `UPDATE credentials
		SET status = 'revoked', ended_at = now() WHERE credential_hash = $1`, secret.Hash(secret.FromString(credentialA))); err != nil {
		t.Fatalf("revoke contract credential: %v", err)
	}
	client.openConnectionRefused(t, credentialA, "credential_revoked")
	for _, route := range []struct {
		operation string
		path      string
	}{
		{operation: "acknowledgeMachine", path: "/api/v1/machines/current/acknowledgment"},
		{operation: "reportSaveFailure", path: "/api/v1/machines/current/save-failure"},
	} {
		response := client.call(t, http.MethodPost, route.path, "", bearerHeaders(credentialA), callOptions{})
		expectStatus(t, response, http.StatusUnauthorized)
		expectJSONField(t, response, "error", "credential_revoked")
	}

	// Host filtering runs before route handlers; send every operation through it.
	for _, request := range []struct {
		operation string
		method    string
		path      string
		body      string
		headers   http.Header
	}{
		{operation: "health", method: http.MethodGet, path: "/health"},
		{operation: "startPairing", method: http.MethodPost, path: "/api/v1/pairings", body: jsonBody(map[string]string{"hostname": "host-check"}), headers: jsonHeaders()},
		{operation: "pollPairing", method: http.MethodGet, path: "/api/v1/pairings/current"},
		{operation: "submitCode", method: http.MethodPost, path: "/api/v1/pairings/current/code", body: jsonBody(map[string]string{"code": "1234-5678"}), headers: jsonHeaders()},
		{operation: "acknowledgeMachine", method: http.MethodPost, path: "/api/v1/machines/current/acknowledgment"},
		{operation: "reportSaveFailure", method: http.MethodPost, path: "/api/v1/machines/current/save-failure"},
		{operation: "openConnection", method: http.MethodGet, path: "/api/v1/machines/current/connection", headers: bearerHeaders(credentialA)},
		{operation: "listMachines", method: http.MethodGet, path: "/api/v1/machines"},
		{operation: "machinesPage", method: http.MethodGet, path: "/machines"},
		{operation: "approvalPage", method: http.MethodGet, path: "/pair/host-check"},
		{operation: "readApproval", method: http.MethodGet, path: "/api/v1/approvals/current"},
		{operation: "acceptApproval", method: http.MethodPost, path: "/api/v1/approvals/current/accept"},
		{operation: "rejectApproval", method: http.MethodPost, path: "/api/v1/approvals/current/reject"},
	} {
		t.Run("unknown_host_"+request.operation, func(t *testing.T) {
			response := client.call(t, request.method, request.path, request.body, request.headers, callOptions{host: "evil.example"})
			expectStatus(t, response, http.StatusForbidden)
			expectJSONField(t, response, "error", "unknown_host")
		})
	}

	assertCoverage(t, doc, client.seen)
}

func TestContractCatchesExtraField(t *testing.T) {
	registerBodyDecoders()
	doc := loadContract(t)
	baseURL := "http://127.0.0.1:8080"
	doc.Servers = openapi3.Servers{&openapi3.Server{URL: baseURL}}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("build contract router: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/pairings/current", nil)
	if err != nil {
		t.Fatal(err)
	}
	route, pathParams, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("find pollPairing route: %v", err)
	}
	requestInput := &openapi3filter.RequestValidationInput{
		Request:     req,
		PathParams:  pathParams,
		QueryParams: req.URL.Query(),
		Route:       route,
		Options:     filterOptions(),
	}
	response := []byte(`{"status":"waiting_for_approval","expires_in_seconds":600,"credential":"unexpected"}`)
	err = validateContractResponse(requestInput, http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, response)
	if err == nil {
		t.Fatal("response with an unlisted credential field passed the contract")
	}
}

func TestCoverageSkipsPlannedOperation(t *testing.T) {
	doc := loadCoverageContract(t)

	problems := coverageProblems(doc, map[string]map[int]bool{})
	assertCoverageProblemPresent(t, problems, "operation unmarkedOperation (GET /unmarked) was never called")
	assertCoverageProblemPresent(t, problems, "operation falsePlannedOperation (GET /false) was never called")
	for _, problem := range problems {
		if strings.Contains(problem, "plannedOperation") {
			t.Errorf("coverage problems = %q, unexpectedly names plannedOperation", problems)
		}
	}
}

func TestCoverageReportsBuiltPlannedOperation(t *testing.T) {
	doc := loadCoverageContract(t)
	seen := map[string]map[int]bool{
		"plannedOperation": {http.StatusOK: true},
	}

	problems := coverageProblems(doc, seen)
	assertCoverageProblemPresent(t, problems, "operation plannedOperation is built but still marked x-planned; remove the mark")
	assertCoverageProblemAbsent(t, problems, "operation plannedOperation (GET /planned) was never called")
	assertCoverageProblemAbsent(t, problems, "operation plannedOperation never returned documented status 200")
}

func TestCoverageUnchangedForUnmarked(t *testing.T) {
	doc := loadCoverageContract(t)
	seen := map[string]map[int]bool{
		"unmarkedOperation": {http.StatusOK: true},
	}

	problems := coverageProblems(doc, seen)
	assertCoverageProblemPresent(t, problems, "operation unmarkedOperation never returned documented status 201")
	assertCoverageProblemPresent(t, problems, "operation unmarkedOperation never returned documented status 400")
	assertCoverageProblemAbsent(t, problems, "operation unmarkedOperation never returned documented status 200")
	assertCoverageProblemAbsent(t, problems, "operation unmarkedOperation (GET /unmarked) was never called")
}

func loadCoverageContract(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info:
  title: Coverage test contract
  version: 1.0.0
paths:
  /unmarked:
    get:
      operationId: unmarkedOperation
      responses:
        "200": {description: OK}
        "201": {description: Created}
        "400": {description: Bad request}
  /planned:
    get:
      operationId: plannedOperation
      x-planned: true
      responses:
        "200": {description: OK}
  /false:
    get:
      operationId: falsePlannedOperation
      x-planned: false
      responses:
        "200": {description: OK}
`))
	if err != nil {
		t.Fatalf("load inline coverage contract: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate inline coverage contract: %v", err)
	}
	return doc
}

func assertCoverageProblemPresent(t *testing.T, problems []string, want string) {
	t.Helper()
	for _, problem := range problems {
		if problem == want {
			return
		}
	}
	t.Errorf("coverage problems = %q, missing %q", problems, want)
}

func assertCoverageProblemAbsent(t *testing.T, problems []string, unwanted string) {
	t.Helper()
	for _, problem := range problems {
		if problem == unwanted {
			t.Errorf("coverage problems = %q, unexpectedly contains %q", problems, unwanted)
		}
	}
}

type contractClient struct {
	server *httptest.Server
	router routers.Router
	seen   map[string]map[int]bool
}

type callOptions struct {
	headers               http.Header
	host                  string
	skipRequestValidation bool
}

type contractResponse struct {
	status int
	body   []byte
}

type startResponse struct {
	PollingKey  string `json:"polling_key"`
	ApprovalURL string `json:"approval_url"`
}

func (c *contractClient) call(t *testing.T, method, path, body string, headers http.Header, options callOptions) contractResponse {
	t.Helper()
	request, err := http.NewRequest(method, c.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s request: %v", method, path, err)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	for name, values := range options.headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if options.host != "" {
		request.Host = options.host
	}

	route, pathParams, err := c.router.FindRoute(request)
	if err != nil {
		t.Fatalf("find contract route for %s %s: %v", method, path, err)
	}
	validationRequest := request.Clone(context.Background())
	validationRequest.Body = io.NopCloser(strings.NewReader(body))
	requestInput := &openapi3filter.RequestValidationInput{
		Request:     validationRequest,
		PathParams:  pathParams,
		QueryParams: validationRequest.URL.Query(),
		Route:       route,
		Options:     filterOptions(),
	}
	if !options.skipRequestValidation {
		if err := openapi3filter.ValidateRequest(context.Background(), requestInput); err != nil {
			t.Fatalf("request violates contract for %s %s (%s): %v", route.Operation.OperationID, method, path, err)
		}
	}

	response, err := c.server.Client().Do(request)
	if err != nil {
		t.Fatalf("send %s %s request: %v", method, path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	if c.seen[route.Operation.OperationID] == nil {
		c.seen[route.Operation.OperationID] = make(map[int]bool)
	}
	c.seen[route.Operation.OperationID][response.StatusCode] = true
	if err := validateContractResponse(requestInput, response.StatusCode, response.Header, responseBody); err != nil {
		t.Fatalf("response violates contract for %s %d (%s %s): %v", route.Operation.OperationID, response.StatusCode, method, path, err)
	}
	return contractResponse{status: response.StatusCode, body: responseBody}
}

func (c *contractClient) openConnection(t *testing.T, credential string) {
	t.Helper()
	path := "/api/v1/machines/current/connection"
	request, err := http.NewRequest(http.MethodGet, c.server.URL+path, nil)
	if err != nil {
		t.Fatalf("build openConnection request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhpcyBpcyBhIDE2LWJ5dGUga2V5")
	route, pathParams, err := c.router.FindRoute(request)
	if err != nil {
		t.Fatalf("find openConnection route: %v", err)
	}
	requestInput := &openapi3filter.RequestValidationInput{
		Request: request, PathParams: pathParams, QueryParams: request.URL.Query(),
		Route: route, Options: filterOptions(),
	}
	if err := openapi3filter.ValidateRequest(context.Background(), requestInput); err != nil {
		t.Fatalf("openConnection request violates contract: %v", err)
	}
	websocketURL := "ws" + strings.TrimPrefix(c.server.URL, "http") + path
	conn, response, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: bearerHeaders(credential)})
	if err != nil {
		t.Fatalf("openConnection WebSocket handshake: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "contract test done")
	if c.seen[route.Operation.OperationID] == nil {
		c.seen[route.Operation.OperationID] = make(map[int]bool)
	}
	c.seen[route.Operation.OperationID][http.StatusSwitchingProtocols] = true
	if err := validateContractResponse(requestInput, http.StatusSwitchingProtocols, response.Header, nil); err != nil {
		t.Fatalf("openConnection response violates contract: %v", err)
	}
}

func (c *contractClient) openConnectionRefused(t *testing.T, credential, wantError string) {
	t.Helper()
	path := "/api/v1/machines/current/connection"
	request, err := http.NewRequest(http.MethodGet, c.server.URL+path, nil)
	if err != nil {
		t.Fatalf("build openConnection request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhpcyBpcyBhIDE2LWJ5dGUga2V5")
	route, pathParams, err := c.router.FindRoute(request)
	if err != nil {
		t.Fatalf("find openConnection route: %v", err)
	}
	requestInput := &openapi3filter.RequestValidationInput{
		Request: request, PathParams: pathParams, QueryParams: request.URL.Query(),
		Route: route, Options: filterOptions(),
	}
	if err := openapi3filter.ValidateRequest(context.Background(), requestInput); err != nil {
		t.Fatalf("openConnection request violates contract: %v", err)
	}
	websocketURL := "ws" + strings.TrimPrefix(c.server.URL, "http") + path
	_, response, err := websocket.Dial(context.Background(), websocketURL, &websocket.DialOptions{HTTPHeader: bearerHeaders(credential)})
	if err == nil {
		t.Fatal("openConnection accepted a revoked credential")
	}
	if response == nil {
		t.Fatalf("openConnection returned no HTTP response: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read openConnection response: %v", err)
	}
	if c.seen[route.Operation.OperationID] == nil {
		c.seen[route.Operation.OperationID] = make(map[int]bool)
	}
	c.seen[route.Operation.OperationID][response.StatusCode] = true
	if err := validateContractResponse(requestInput, response.StatusCode, response.Header, body); err != nil {
		t.Fatalf("openConnection response violates contract: %v", err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("openConnection status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	var errorBody map[string]string
	if err := json.Unmarshal(body, &errorBody); err != nil {
		t.Fatalf("decode openConnection error: %v", err)
	}
	if errorBody["error"] != wantError {
		t.Fatalf("openConnection error = %q, want %q", errorBody["error"], wantError)
	}
}

func validateContractResponse(requestInput *openapi3filter.RequestValidationInput, status int, headers http.Header, body []byte) error {
	return openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: requestInput,
		Status:                 status,
		Header:                 headers,
		Body:                   io.NopCloser(bytes.NewReader(body)),
		Options:                filterOptions(),
	})
}

func filterOptions() *openapi3filter.Options {
	return &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, IncludeResponseStatus: true}
}

func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatalf("load api.OpenAPI: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate api.OpenAPI: %v", err)
	}
	return doc
}

func (c *contractClient) startPairing(t *testing.T, hostname string) startResponse {
	t.Helper()
	response := c.call(t, http.MethodPost, "/api/v1/pairings", jsonBody(map[string]string{"hostname": hostname, "os_name": "linux", "os_version": "1"}), jsonHeaders(), callOptions{})
	expectStatus(t, response, http.StatusCreated)
	var started startResponse
	if err := json.Unmarshal(response.body, &started); err != nil {
		t.Fatalf("decode startPairing response: %v", err)
	}
	if started.PollingKey == "" || started.ApprovalURL == "" {
		t.Fatal("startPairing response omitted required secrets")
	}
	return started
}

func approvalPath(t *testing.T, approvalURL string) string {
	t.Helper()
	parsed, err := url.Parse(approvalURL)
	if err != nil {
		t.Fatalf("parse approval URL: %v", err)
	}
	if !strings.HasPrefix(parsed.Path, "/pair/") {
		t.Fatalf("approval URL path = %q, want /pair/<key>", parsed.Path)
	}
	return parsed.Path
}

func approvalKey(t *testing.T, approvalURL string) string {
	t.Helper()
	path := approvalPath(t, approvalURL)
	return strings.TrimPrefix(path, "/pair/")
}

func bearerHeaders(proof string) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+proof)
	return headers
}

func jsonHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return headers
}

func jsonBody(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func expectStatus(t *testing.T, response contractResponse, expected int) {
	t.Helper()
	if response.status != expected {
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(response.body, &body); err != nil {
			t.Fatalf("status = %d, want %d; error = <unavailable>", response.status, expected)
		}
		t.Fatalf("status = %d, want %d; error = %q", response.status, expected, body.Error)
	}
}

func jsonField(t *testing.T, response contractResponse, name string) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
	value, ok := body[name].(string)
	if !ok {
		t.Fatalf("response field %q = %#v, want string", name, body[name])
	}
	return value
}

func expectJSONField(t *testing.T, response contractResponse, name string, expected any) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
	if got := body[name]; got != expected {
		t.Fatalf("response field %q = %#v, want %#v", name, got, expected)
	}
}

func expectJSONFieldPresent(t *testing.T, response contractResponse, name string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
	if _, ok := body[name]; !ok {
		t.Fatalf("response omitted field %q", name)
	}
}

func wrongCode(code string) string {
	for index, digit := range code {
		if digit >= '0' && digit <= '9' {
			replacement := byte('0' + (digit-'0'+1)%10)
			return code[:index] + string(replacement) + code[index+1:]
		}
	}
	return "0000-0000"
}

func assertCoverage(t *testing.T, doc *openapi3.T, seen map[string]map[int]bool) {
	t.Helper()
	for _, problem := range coverageProblems(doc, seen) {
		t.Errorf("%s", problem)
	}
}

func coverageProblems(doc *openapi3.T, seen map[string]map[int]bool) []string {
	var problems []string
	for path, item := range doc.Paths.Map() {
		for method, operation := range item.Operations() {
			operationID := operation.OperationID
			statuses := seen[operationID]
			if planned, ok := operation.Extensions["x-planned"].(bool); ok && planned {
				if len(statuses) > 0 {
					problems = append(problems, "operation "+operationID+" is built but still marked x-planned; remove the mark")
				}
				continue
			}
			if len(statuses) == 0 {
				problems = append(problems, "operation "+operationID+" ("+method+" "+path+") was never called")
			}
			for statusCode := range operation.Responses.Map() {
				status, err := strconv.Atoi(statusCode)
				if err != nil || status == http.StatusInternalServerError {
					continue
				}
				if !statuses[status] {
					problems = append(problems, "operation "+operationID+" never returned documented status "+strconv.Itoa(status))
				}
			}
		}
	}
	return problems
}

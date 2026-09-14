package jumpcloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/go-resty/resty/v2"
)

// TestGetUserGroupIDsUsesMemberOfEndpoint is a regression test for getUserGroupIDs
// calling GraphUserAssociationsList (a graph-of-direct-associations endpoint whose
// "targets" filter doesn't even accept "user_group" as a value) instead of
// GraphUserMemberOf (the endpoint that actually returns a user's group memberships).
// It fakes GET /users/{id}/memberof and asserts getUserGroupIDs extracts the group
// IDs from the returned GraphObjectWithPaths list.
func TestGetUserGroupIDsUsesMemberOfEndpoint(t *testing.T) {
	const userID = "user-1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/"+userID+"/memberof" {
			t.Errorf("expected request to /users/%s/memberof, got %s", userID, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]jcapiv2.GraphObjectWithPaths{
			{Id: "group-a"},
			{Id: "group-b"},
		})
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	groupIDs, err := getUserGroupIDs(client, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]bool{"group-a": true, "group-b": true}
	if len(groupIDs) != len(want) {
		t.Fatalf("expected %d group IDs, got %v", len(want), groupIDs)
	}
	for _, id := range groupIDs {
		if !want[id] {
			t.Errorf("unexpected group ID %q in result %v", id, groupIDs)
		}
	}
}

// TestGetUserGroupIDsReturnsErrUserNotFoundOn404 is a regression test for getUserGroupIDs
// swallowing a 404 into a nil-error empty-slice return, which made "user deleted" indistinguishable
// from "user has zero groups" from the caller's point of view.
func TestGetUserGroupIDsReturnsErrUserNotFoundOn404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	_, err := getUserGroupIDs(client, "deleted-user")
	if err == nil {
		t.Fatal("expected an error for a deleted user, got nil")
	}
	if !errors.Is(err, errUserNotFound) {
		t.Errorf("expected error to wrap errUserNotFound, got: %v", err)
	}
}

// testRestyLogger is a minimal resty.Logger that records everything it's asked to log.
type testRestyLogger struct {
	buf *bytes.Buffer
}

func (l testRestyLogger) Errorf(format string, v ...interface{}) { fmt.Fprintf(l.buf, format, v...) }
func (l testRestyLogger) Warnf(format string, v ...interface{})  { fmt.Fprintf(l.buf, format, v...) }
func (l testRestyLogger) Debugf(format string, v ...interface{}) { fmt.Fprintf(l.buf, format, v...) }

// TestApplicationMetadataClientDoesNotEnableDebugLogging is a regression test for the API key
// leak: GetApplicationMetadataXml used to unconditionally enable resty debug mode, which logs
// the full request - including the x-api-key header - to the log stream any time TF_LOG is set.
//
// GetApplicationMetadataXml's target URL is hardcoded to the real JumpCloud API and isn't
// injectable, so this can't drive that exact function end-to-end against a local server. Instead
// it builds a resty client the same way the fixed function does (resty.New(), no SetDebug) and
// proves that a request carrying the header produces zero debug log output - i.e. the header
// can't leak - whereas the old SetDebug(true) construction would have logged it.
func TestApplicationMetadataClientDoesNotEnableDebugLogging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<xml/>"))
	}))
	defer server.Close()

	var logOutput bytes.Buffer
	client := resty.New()
	client.SetLogger(testRestyLogger{buf: &logOutput})

	const apiKey = "super-secret-api-key"
	if _, err := client.R().SetHeader("x-api-key", apiKey).Get(server.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.Debug {
		t.Error("expected Debug to be false, matching GetApplicationMetadataXml's client construction")
	}
	if strings.Contains(logOutput.String(), apiKey) {
		t.Errorf("API key leaked into log output: %s", logOutput.String())
	}
}

// redirectTransport rewrites every outgoing request's scheme/host to target, so a function with
// a hardcoded base URL (e.g. one built via convertV2toV1Config, which always points at the real
// JumpCloud API regardless of the *jcapiv2.Configuration passed in) can be tested against a local
// httptest server without changing its signature.
type redirectTransport struct {
	target   *url.URL
	delegate http.RoundTripper
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return rt.delegate.RoundTrip(req)
}

// withRedirectedDefaultTransport temporarily points http.DefaultTransport (what jcapi-go clients
// fall back to when no HTTPClient is configured) at target for the duration of the test.
func withRedirectedDefaultTransport(t *testing.T, target string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parsing redirect target %q: %v", target, err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = &redirectTransport{target: u, delegate: original}
	t.Cleanup(func() { http.DefaultTransport = original })
}

// TestUserIDsToEmailsHandlesExtraResultsWithoutPanic is a regression test for the index-out-of-
// range panic in userIDsToEmails: the old code pre-sized its result slice by input ID count and
// wrote into it at a computed index, so a page returning more rows than requested IDs panicked.
func TestUserIDsToEmailsHandlesExtraResultsWithoutPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("userIDsToEmails panicked: %v", r)
		}
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jcapiv1.Systemuserslist{
			Results: []jcapiv1.Systemuserreturn{
				{Email: "a@example.com"},
				{Email: "b@example.com"},
			},
		})
	}))
	defer server.Close()
	withRedirectedDefaultTransport(t, server.URL)

	emails, err := userIDsToEmails(jcapiv2.NewConfiguration(), []string{"only-one-id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(emails) != 2 {
		t.Errorf("expected 2 emails (server returned more than the 1 requested), got %v", emails)
	}
}

// TestUserEmailsToIDsHandlesExtraResultsWithoutPanic mirrors the above for userEmailsToIDs, which
// has the identical indexed-write bug pattern.
func TestUserEmailsToIDsHandlesExtraResultsWithoutPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("userEmailsToIDs panicked: %v", r)
		}
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jcapiv1.Systemuserslist{
			Results: []jcapiv1.Systemuserreturn{
				{Id: "id-a"},
				{Id: "id-b"},
			},
		})
	}))
	defer server.Close()
	withRedirectedDefaultTransport(t, server.URL)

	ids, err := userEmailsToIDs(jcapiv2.NewConfiguration(), []interface{}{"only-one@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 ids (server returned more than the 1 requested), got %v", ids)
	}
}

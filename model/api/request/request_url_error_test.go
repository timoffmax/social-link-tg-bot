package request

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSendTransportErrorLeavesTokenOut(t *testing.T) {
	const token = "123456:secret-test-token"
	t.Setenv("TG_BOT_TOKEN", token)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	previous := SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")
	defer SetAPIURLTemplate(previous)

	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	_, err := (&AuthorizedRequest{ApiMethod: "getUpdates", RequestMethod: RequestMethodGET, RequestContentType: ContentTypeJson}).Send(nil)

	if err == nil {
		t.Fatalf("a closed server must fail the request")
	}

	if strings.Contains(err.Error(), token) || strings.Contains(logged.String(), token) {
		t.Fatalf("token leaked: err=%q log=%q", err.Error(), logged.String())
	}

	if strings.Contains(err.Error(), "getUpdates") == false {
		t.Fatalf("error should name the method: %q", err.Error())
	}
}

func TestSendBuildErrorLeavesTokenOut(t *testing.T) {
	const token = "123456:secret-test-token\r"
	t.Setenv("TG_BOT_TOKEN", token)

	_, err := (&AuthorizedRequest{ApiMethod: "getUpdates", RequestMethod: RequestMethodPOST, RequestContentType: ContentTypeJson}).Send(nil)

	if (err == nil) || strings.Contains(err.Error(), "secret-test-token") {
		t.Fatalf("expected an error without the token, got %v", err)
	}
}

func TestWithoutRequestURLKeepsOtherErrors(t *testing.T) {
	plain := errors.New("plain")

	if WithoutRequestURL("getMe", nil) != nil {
		t.Fatalf("nil must stay nil")
	}

	if WithoutRequestURL("getMe", plain) != plain {
		t.Fatalf("a non-URL error must be returned unchanged")
	}
}

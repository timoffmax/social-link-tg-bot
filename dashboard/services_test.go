package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const testServicesData = `{"version":1,"services":[{"key":"kkinstagram","state":"degraded"}]}`

func TestServicesComponentStaleOnlyPastThreeMinutes(t *testing.T) {
	cases := []struct {
		age     time.Duration
		isStale bool
	}{
		{0, false},
		{heartbeatStaleAfter, false},
		{heartbeatStaleAfter + time.Second, true},
	}

	for _, c := range cases {
		components := map[string]StatusComponent{
			componentServices: {Data: json.RawMessage(testServicesData), UpdatedAt: testNow.Add(-c.age)},
		}
		got := publishedComponent(components, componentServices, testNow)

		if (got == nil) || (got.IsStale != c.isStale) || (string(got.Data) != testServicesData) {
			t.Errorf("age %s: got %+v", c.age, got)
		}
	}
}

func TestServicesComponentNilWhenNeverPublished(t *testing.T) {
	components := map[string]StatusComponent{
		componentPaid: {Data: json.RawMessage(`{}`), UpdatedAt: testNow},
	}

	if got := publishedComponent(components, componentServices, testNow); got != nil {
		t.Errorf("got %+v", got)
	}
}

func TestPlatformsServicesPassedThroughByteForByte(t *testing.T) {
	store := &stubStore{services: &PublishedComponent{Data: json.RawMessage(testServicesData), UpdatedAt: "2026-10-15T11:59:00Z"}}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/platforms")
	want := []byte(fmt.Sprintf(`"services":{"data":%s,"updated_at":"2026-10-15T11:59:00Z","is_stale":false}`, testServicesData))

	if (recorder.Code != http.StatusOK) || (bytes.Contains(recorder.Body.Bytes(), want) == false) {
		t.Errorf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestPlatformsServicesNullWhenNeverPublished(t *testing.T) {
	recorder := doRequest(newTestServer(&stubStore{}), http.MethodGet, "/api/platforms")
	body := decodeBody(t, recorder)
	services, isPresent := body["services"]

	if (recorder.Code != http.StatusOK) || (isPresent == false) || (services != nil) {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestPlatformsStaysGetOnly(t *testing.T) {
	store := &stubStore{}
	server := newTestServer(store)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		recorder := doRequest(server, method, "/api/platforms")

		if (recorder.Code != http.StatusMethodNotAllowed) || (store.calls != 0) {
			t.Errorf("%s: status = %d, calls = %d", method, recorder.Code, store.calls)
		}
	}
}

func TestServicesOrNilDropsAFailedRead(t *testing.T) {
	services := &PublishedComponent{Data: json.RawMessage(testServicesData)}

	if servicesOrNil(services, errors.New("permission denied for table bot_runtime_status")) != nil {
		t.Fatalf("a failed read must give nil")
	}

	if servicesOrNil(services, nil) != services {
		t.Fatalf("a read must pass the component through")
	}
}

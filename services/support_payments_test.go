package services

import (
	"errors"
	"strings"
	"testing"
)

func TestSupportTiersAreTheThreeFixedPrices(t *testing.T) {
	tiers := SupportTiers()
	expected := []SupportTier{{Stars: 25, Credits: 10}, {Stars: 50, Credits: 25}, {Stars: 100, Credits: 60}}

	if len(tiers) != len(expected) {
		t.Fatalf("expected %d tiers, got %+v", len(expected), tiers)
	}

	for i, tier := range expected {
		if tiers[i] != tier {
			t.Fatalf("tier %d: expected %+v, got %+v", i, tier, tiers[i])
		}
	}
}

func TestSupportPayloadRoundTrips(t *testing.T) {
	tier, _ := FindSupportTier(50)
	payload := BuildSupportPayload(tier, 4242)

	invoice, err := ParseSupportPayload(payload)

	if (payload != "support:50:4242") || (err != nil) || (invoice.Tier != tier) || (invoice.UserID != 4242) {
		t.Fatalf("unexpected round trip: %q %+v %v", payload, invoice, err)
	}
}

func TestParseSupportPayloadRejectsUnknownOrMalformedPayloads(t *testing.T) {
	payloads := []string{"", "support:30:42", "support:50", "support:50:0", "support:50:abc", "tip:50:42", "support:50:42:1", "support:x:42"}

	for _, payload := range payloads {
		_, err := ParseSupportPayload(payload)

		if errors.Is(err, ErrSupportPayloadInvalid) == false {
			t.Fatalf("expected %q to be rejected, got %v", payload, err)
		}
	}
}

func TestGatedCreditNoteNamesThePool(t *testing.T) {
	caption := gatedCreditNote(gatedCredit{IsUsed: true, Left: 9})
	groupCaption := gatedCreditNote(gatedCredit{IsUsed: true, IsGroup: true, Left: 4})

	if strings.HasSuffix(caption, "🎟️ Bonus downloads left: 9") == false {
		t.Fatalf("unexpected caption %q", caption)
	}

	if strings.HasSuffix(groupCaption, "🚀 Group bonus downloads left: 4") == false {
		t.Fatalf("unexpected group caption %q", groupCaption)
	}
}

func TestBoostPayloadRoundTripsAndNeedsAGroup(t *testing.T) {
	tier, _ := FindSupportTier(50)
	invoice, err := ParseSupportPayload(BuildBoostPayload(tier, 42, -1001234))

	if (err != nil) || (invoice.BoostChatID != -1001234) || (invoice.UserID != 42) || (invoice.Tier.Credits != tier.Credits) {
		t.Fatalf("unexpected boost invoice %+v (%v)", invoice, err)
	}

	for _, payload := range []string{"boost:50:42:777", "boost:50:42", "boost:51:42:-1", "boost:50:0:-1"} {
		if _, err := ParseSupportPayload(payload); err == nil {
			t.Fatalf("expected %q to be rejected", payload)
		}
	}

	personal, err := ParseSupportPayload(BuildSupportPayload(tier, 42))

	if (err != nil) || (personal.BoostChatID != 0) {
		t.Fatalf("personal support must not boost a group: %+v (%v)", personal, err)
	}
}

func TestSupportStoreFailsClosedWithoutDatabase(t *testing.T) {
	previous := dbService
	dbService = nil

	t.Cleanup(func() {
		dbService = previous
	})

	_, recordErr := RecordSupportPayment(SupportPayment{ChargeID: "c", UserID: 1, Stars: 25, Credits: 10})
	isTaken, _, takeErr := TakeGatedFetchCredit(1)
	_, refundErr := MarkSupportPaymentRefunded(1, "c")

	if (recordErr == nil) || (takeErr == nil) || (isTaken == true) || (refundErr == nil) {
		t.Fatalf("expected every call to fail without a database: %v %v %v %v", recordErr, isTaken, takeErr, refundErr)
	}
}

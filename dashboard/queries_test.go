package dashboard

import (
	"database/sql"
	"testing"
)

func TestGatedLimitMapsTheStoredMonthlyLimit(t *testing.T) {
	limit := int64(20)
	zero := int64(0)
	cases := []struct {
		name          string
		stored        sql.NullInt64
		wantLimit     *int64
		wantUnlimited bool
	}{
		{name: "default", stored: sql.NullInt64{}, wantLimit: nil, wantUnlimited: false},
		{name: "custom", stored: sql.NullInt64{Int64: limit, Valid: true}, wantLimit: &limit, wantUnlimited: false},
		{name: "off", stored: sql.NullInt64{Int64: zero, Valid: true}, wantLimit: &zero, wantUnlimited: false},
		{name: "unlimited", stored: sql.NullInt64{Int64: gatedUnlimited, Valid: true}, wantLimit: nil, wantUnlimited: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLimit, gotUnlimited := gatedLimit(tc.stored)
			assertEqual(t, "limit", gotLimit, tc.wantLimit)
			assertEqual(t, "unlimited", gotUnlimited, tc.wantUnlimited)
		})
	}
}

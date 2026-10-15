package credential

import "testing"

func TestRevokeSQL(t *testing.T) {
	for _, test := range []struct {
		column string
		want   string
	}{
		{
			column: "machine_id",
			want:   "UPDATE credentials SET status = 'revoked', ended_at = now()\n\tWHERE machine_id = $1 AND status = 'active'",
		},
		{
			column: "credential_id",
			want:   "UPDATE credentials SET status = 'revoked', ended_at = now()\n\tWHERE credential_id = $1 AND status = 'active'",
		},
	} {
		t.Run(test.column, func(t *testing.T) {
			if got := revokeSQL(test.column); got != test.want {
				t.Errorf("revokeSQL(%q) = %q, want %q", test.column, got, test.want)
			}
		})
	}
}

package pairing

// effectiveStatus is the request's status as seen now: past its deadline, a
// waiting request counts as expired and an unconfirmed machine as failed.
// The query must name the tables pr and m, with LEFT JOIN machines AS m.
const effectiveStatus = `CASE
	WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now() THEN 'expired'
	WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now() THEN 'failed'
	ELSE pr.status
END`

// effectiveFailureReason is the failure reason that goes with effectiveStatus.
const effectiveFailureReason = `CASE WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now()
	THEN 'not_confirmed' ELSE coalesce(pr.failure_reason, '') END`

package analytics

import (
	"time"

	"github.com/jackc/pgx/v5"
)

// GuestConversion counts guest browser identities, not attempts or all anonymous
// site visitors. Daily rows are cohorts by guest_trials.created_at: later stages
// always belong to those same guests, observed through GeneratedAt.
type GuestConversion struct {
	Guests     int64                  `json:"guests"`
	Started    int64                  `json:"started"`
	Completed  int64                  `json:"completed"`
	Registered int64                  `json:"registered"`
	Daily      []GuestConversionPoint `json:"daily"`
}

type GuestConversionPoint struct {
	Day        string `json:"day"`
	Guests     int64  `json:"guests"`
	Started    int64  `json:"started"`
	Completed  int64  `json:"completed"`
	Registered int64  `json:"registered"`
}

func queueGuestConversion(batch *pgx.Batch, result *GuestConversion, from, today, now time.Time) {
	// Claim transfers completed sessions to the account. Include the linked
	// claimed session as well as sessions still owned by the guest; never include
	// unrelated account mocks. MIN avoids inflating the funnel with retakes.
	// Registration is attributed only when the linked account became REGISTERED
	// after the mock was submitted. Signing into an older account is not conversion.
	batch.Queue(`
		WITH guests AS (
			SELECT (g.created_at AT TIME ZONE $3)::date AS day,
				m.started_at IS NOT NULL AS started,
				m.completed_at IS NOT NULL AS completed,
				(u.status = 'REGISTERED' AND m.completed_at IS NOT NULL
					AND COALESCE(u.terms_accepted_at, u.created_at) >= m.completed_at
					AND COALESCE(u.terms_accepted_at, u.created_at) <= $4) AS registered
			FROM guest_trials g
			LEFT JOIN users u ON u.id = g.claimed_by
			LEFT JOIN LATERAL (
				SELECT MIN(s.started_at) AS started_at,
					MIN(s.submitted_at) FILTER (WHERE s.status = 'SUBMITTED'
						AND s.submitted_at >= s.started_at AND s.submitted_at <= $4) AS completed_at
				FROM (
					SELECT started_at, submitted_at, status FROM full_mock_sessions WHERE user_id = g.user_id
					UNION ALL
					SELECT started_at, submitted_at, status FROM full_mock_sessions WHERE id = g.claimed_session_id
				) s
				WHERE s.started_at >= g.created_at AND s.started_at <= $4
			) m ON true
			WHERE g.created_at >= $1 AND g.created_at <= $4
		), counts AS (
			SELECT day, COUNT(*) AS guests,
				COUNT(*) FILTER (WHERE started) AS started,
				COUNT(*) FILTER (WHERE completed) AS completed,
				COUNT(*) FILTER (WHERE registered) AS registered
			FROM guests GROUP BY day
		)
		SELECT to_char(d.day, 'YYYY-MM-DD'), COALESCE(c.guests, 0),
			COALESCE(c.started, 0), COALESCE(c.completed, 0), COALESCE(c.registered, 0)
		FROM generate_series($5::date, $2::date, interval '1 day') d(day)
		LEFT JOIN counts c ON c.day = d.day::date
		ORDER BY d.day
	`, from, today.Format(time.DateOnly), TimeZone, now, from.Format(time.DateOnly)).Query(func(rows pgx.Rows) error {
		*result = GuestConversion{Daily: []GuestConversionPoint{}}
		for rows.Next() {
			var p GuestConversionPoint
			if err := rows.Scan(&p.Day, &p.Guests, &p.Started, &p.Completed, &p.Registered); err != nil {
				return err
			}
			result.Daily = append(result.Daily, p)
			result.Guests += p.Guests
			result.Started += p.Started
			result.Completed += p.Completed
			result.Registered += p.Registered
		}
		return rows.Err()
	})
}

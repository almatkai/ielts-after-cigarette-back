package analytics

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Totals struct {
	RegisteredUsers    int64 `json:"registeredUsers"`
	WaitlistPending    int64 `json:"waitlistPending"`
	WaitlistLeads      int64 `json:"waitlistLeads"`
	WaitlistConverted  int64 `json:"waitlistConverted"`
	RegisteredToday    int64 `json:"registeredToday"`
	Registered7d       int64 `json:"registered7d"`
	Registered30d      int64 `json:"registered30d"`
	WaitlistToday      int64 `json:"waitlistToday"`
	Waitlist7d         int64 `json:"waitlist7d"`
	DAU                int64 `json:"dau"`
	WAU                int64 `json:"wau"`
	MAU                int64 `json:"mau"`
	AttemptsToday      int64 `json:"attemptsToday"`
	SubmittedToday     int64 `json:"submittedToday"`
	FullMocksStarted   int64 `json:"fullMocksStarted"`
	FullMocksSubmitted int64 `json:"fullMocksSubmitted"`
}

type DailyPoint struct {
	Day           string `json:"day"`
	Registrations int64  `json:"registrations"`
	WaitlistJoins int64  `json:"waitlistJoins"`
	ActiveUsers   int64  `json:"activeUsers"`
	Attempts      int64  `json:"attempts"`
	Submitted     int64  `json:"submitted"`
	Visitors      int64  `json:"visitors"`
	PageViews     int64  `json:"pageViews"`
}

type SkillStats struct {
	Skill             string   `json:"skill"`
	Started           int64    `json:"started"`
	Submitted         int64    `json:"submitted"`
	Abandoned         int64    `json:"abandoned"`
	Users             int64    `json:"users"`
	AverageBand       *float64 `json:"averageBand"`
	MedianDurationSec *float64 `json:"medianDurationSec"`
}

type FunnelStep struct {
	Step  string `json:"step"`
	Users int64  `json:"users"`
}

type SourceCount struct {
	Source string `json:"source"`
	Users  int64  `json:"users"`
}

type HeatCell struct {
	Weekday  int   `json:"weekday"` // 1 = Monday … 7 = Sunday
	Hour     int   `json:"hour"`
	Attempts int64 `json:"attempts"`
}

type Cohort struct {
	Week string `json:"week"`
	Size int64  `json:"size"`
	// Retained[i] = users active during week i+1 after registering.
	Retained []int64 `json:"retained"`
}

type Overview struct {
	Days        int           `json:"days"`
	TimeZone    string        `json:"timeZone"`
	Totals      Totals        `json:"totals"`
	Daily       []DailyPoint  `json:"daily"`
	Skills      []SkillStats  `json:"skills"`
	Funnel      []FunnelStep  `json:"funnel"`
	Sources     []SourceCount `json:"sources"`
	Heatmap     []HeatCell    `json:"heatmap"`
	Cohorts     []Cohort      `json:"cohorts"`
	GeneratedAt time.Time     `json:"generatedAt"`
}

const cohortWeeks = 8

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Overview sends every aggregate in one batch: one network round trip, and
// each query is an index range scan over the requested window.
func (r *Repository) Overview(ctx context.Context, now time.Time, days int) (Overview, error) {
	local := now.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	from := today.AddDate(0, 0, -(days - 1))
	cohortFrom := startOfWeek(today).AddDate(0, 0, -7*(cohortWeeks-1))
	result := Overview{Days: days, TimeZone: TimeZone, GeneratedAt: now.UTC()}

	batch := &pgx.Batch{}
	batch.Queue(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'REGISTERED'),
			COUNT(*) FILTER (WHERE status IN ('WAITING', 'INVITED')),
			COUNT(*) FILTER (WHERE referral_code IS NOT NULL),
			COUNT(*) FILTER (WHERE referral_code IS NOT NULL AND status = 'REGISTERED'),
			COUNT(*) FILTER (WHERE status = 'REGISTERED' AND COALESCE(terms_accepted_at, created_at) >= $1),
			COUNT(*) FILTER (WHERE status = 'REGISTERED' AND COALESCE(terms_accepted_at, created_at) >= $2),
			COUNT(*) FILTER (WHERE status = 'REGISTERED' AND COALESCE(terms_accepted_at, created_at) >= $3),
			COUNT(*) FILTER (WHERE referral_code IS NOT NULL AND created_at >= $1),
			COUNT(*) FILTER (WHERE referral_code IS NOT NULL AND created_at >= $2)
		FROM users
	`, today, today.AddDate(0, 0, -6), today.AddDate(0, 0, -29)).QueryRow(func(row pgx.Row) error {
		t := &result.Totals
		return row.Scan(&t.RegisteredUsers, &t.WaitlistPending, &t.WaitlistLeads, &t.WaitlistConverted,
			&t.RegisteredToday, &t.Registered7d, &t.Registered30d, &t.WaitlistToday, &t.Waitlist7d)
	})
	batch.Queue(`
		SELECT
			COUNT(DISTINCT user_id) FILTER (WHERE day = $1::date),
			COUNT(DISTINCT user_id) FILTER (WHERE day > $1::date - 7),
			COUNT(DISTINCT user_id)
		FROM user_activity_days
		WHERE day > $1::date - 30
	`, today.Format(time.DateOnly)).QueryRow(func(row pgx.Row) error {
		return row.Scan(&result.Totals.DAU, &result.Totals.WAU, &result.Totals.MAU)
	})
	batch.Queue(`
		SELECT
			(SELECT COUNT(*) FROM attempts WHERE started_at >= $1),
			(SELECT COUNT(*) FROM attempts WHERE submitted_at >= $1 AND status = 'SUBMITTED'),
			(SELECT COUNT(*) FROM full_mock_sessions WHERE started_at >= $2),
			(SELECT COUNT(*) FROM full_mock_sessions WHERE started_at >= $2 AND status = 'SUBMITTED')
	`, today, from).QueryRow(func(row pgx.Row) error {
		t := &result.Totals
		return row.Scan(&t.AttemptsToday, &t.SubmittedToday, &t.FullMocksStarted, &t.FullMocksSubmitted)
	})
	batch.Queue(`
		WITH days AS (
			SELECT generate_series($1::date, $2::date, interval '1 day')::date AS day
		), registrations AS (
			SELECT (COALESCE(terms_accepted_at, created_at) AT TIME ZONE $3)::date AS day, COUNT(*) AS n
			FROM users
			WHERE status = 'REGISTERED' AND COALESCE(terms_accepted_at, created_at) >= $4
			GROUP BY 1
		), waitlist AS (
			SELECT (created_at AT TIME ZONE $3)::date AS day, COUNT(*) AS n
			FROM users
			WHERE referral_code IS NOT NULL AND created_at >= $4
			GROUP BY 1
		), active AS (
			SELECT day, COUNT(*) AS n FROM user_activity_days WHERE day >= $1::date GROUP BY 1
		), started AS (
			SELECT (started_at AT TIME ZONE $3)::date AS day, COUNT(*) AS n
			FROM attempts WHERE started_at >= $4 GROUP BY 1
		), submitted AS (
			SELECT (submitted_at AT TIME ZONE $3)::date AS day, COUNT(*) AS n
			FROM attempts WHERE submitted_at >= $4 AND status = 'SUBMITTED' GROUP BY 1
		)
		SELECT to_char(days.day, 'YYYY-MM-DD'),
			COALESCE(registrations.n, 0), COALESCE(waitlist.n, 0), COALESCE(active.n, 0),
			COALESCE(started.n, 0), COALESCE(submitted.n, 0)
		FROM days
		LEFT JOIN registrations USING (day)
		LEFT JOIN waitlist USING (day)
		LEFT JOIN active USING (day)
		LEFT JOIN started USING (day)
		LEFT JOIN submitted USING (day)
		ORDER BY days.day
	`, from.Format(time.DateOnly), today.Format(time.DateOnly), TimeZone, from).Query(func(rows pgx.Rows) error {
		result.Daily = make([]DailyPoint, 0, days)
		for rows.Next() {
			var p DailyPoint
			if err := rows.Scan(&p.Day, &p.Registrations, &p.WaitlistJoins, &p.ActiveUsers, &p.Attempts, &p.Submitted); err != nil {
				return err
			}
			result.Daily = append(result.Daily, p)
		}
		return rows.Err()
	})
	batch.Queue(`
		SELECT material_type,
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'SUBMITTED'),
			COUNT(*) FILTER (WHERE status = 'ABANDONED'),
			COUNT(DISTINCT user_id),
			ROUND(AVG(band) FILTER (WHERE status = 'SUBMITTED')::numeric, 2)::float8,
			(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM submitted_at - started_at))
				FILTER (WHERE status = 'SUBMITTED'))::float8
		FROM attempts
		WHERE started_at >= $1
		GROUP BY material_type
		ORDER BY material_type
	`, from).Query(func(rows pgx.Rows) error {
		result.Skills = []SkillStats{}
		for rows.Next() {
			var s SkillStats
			if err := rows.Scan(&s.Skill, &s.Started, &s.Submitted, &s.Abandoned, &s.Users, &s.AverageBand, &s.MedianDurationSec); err != nil {
				return err
			}
			result.Skills = append(result.Skills, s)
		}
		return rows.Err()
	})
	batch.Queue(`
		WITH registered AS (
			SELECT id FROM users
			WHERE status = 'REGISTERED' AND COALESCE(terms_accepted_at, created_at) >= $1
		)
		SELECT
			(SELECT COUNT(*) FROM users WHERE referral_code IS NOT NULL AND created_at >= $1),
			(SELECT COUNT(*) FROM registered),
			(SELECT COUNT(*) FROM registered r WHERE EXISTS (SELECT 1 FROM attempts a WHERE a.user_id = r.id)),
			(SELECT COUNT(*) FROM registered r WHERE EXISTS (SELECT 1 FROM attempts a WHERE a.user_id = r.id AND a.status = 'SUBMITTED')),
			(SELECT COUNT(*) FROM registered r WHERE EXISTS (SELECT 1 FROM full_mock_sessions f WHERE f.user_id = r.id))
	`, from).QueryRow(func(row pgx.Row) error {
		values := make([]int64, 5)
		if err := row.Scan(&values[0], &values[1], &values[2], &values[3], &values[4]); err != nil {
			return err
		}
		result.Funnel = []FunnelStep{
			{"waitlist", values[0]},
			{"registered", values[1]},
			{"started_attempt", values[2]},
			{"submitted_attempt", values[3]},
			{"started_full_mock", values[4]},
		}
		return nil
	})
	batch.Queue(`
		SELECT COALESCE(NULLIF(BTRIM(LOWER(source)), ''), 'direct'), COUNT(*)
		FROM users
		WHERE created_at >= $1
		GROUP BY 1
		ORDER BY 2 DESC, 1
		LIMIT 8
	`, from).Query(func(rows pgx.Rows) error {
		result.Sources = []SourceCount{}
		for rows.Next() {
			var s SourceCount
			if err := rows.Scan(&s.Source, &s.Users); err != nil {
				return err
			}
			result.Sources = append(result.Sources, s)
		}
		return rows.Err()
	})
	batch.Queue(`
		SELECT EXTRACT(ISODOW FROM started_at AT TIME ZONE $2)::int,
			EXTRACT(HOUR FROM started_at AT TIME ZONE $2)::int,
			COUNT(*)
		FROM attempts
		WHERE started_at >= $1
		GROUP BY 1, 2
	`, from, TimeZone).Query(func(rows pgx.Rows) error {
		result.Heatmap = []HeatCell{}
		for rows.Next() {
			var c HeatCell
			if err := rows.Scan(&c.Weekday, &c.Hour, &c.Attempts); err != nil {
				return err
			}
			result.Heatmap = append(result.Heatmap, c)
		}
		return rows.Err()
	})
	batch.Queue(`
		WITH cohort AS (
			SELECT id, (COALESCE(terms_accepted_at, created_at) AT TIME ZONE $2)::date AS reg_day
			FROM users
			WHERE status = 'REGISTERED' AND COALESCE(terms_accepted_at, created_at) >= $1
		), sizes AS (
			SELECT date_trunc('week', reg_day)::date AS week, COUNT(*) AS size FROM cohort GROUP BY 1
		), retained AS (
			SELECT date_trunc('week', c.reg_day)::date AS week,
				(a.day - c.reg_day - 1) / 7 + 1 AS offset_week,
				COUNT(DISTINCT c.id) AS users
			FROM cohort c
			JOIN user_activity_days a ON a.user_id = c.id AND a.day > c.reg_day AND a.day <= c.reg_day + $3::int * 7
			GROUP BY 1, 2
		)
		SELECT to_char(sizes.week, 'YYYY-MM-DD'), sizes.size, COALESCE(retained.offset_week, 0), COALESCE(retained.users, 0)
		FROM sizes LEFT JOIN retained USING (week)
		ORDER BY sizes.week, 3
	`, cohortFrom, TimeZone, cohortWeeks-1).Query(func(rows pgx.Rows) error {
		result.Cohorts = []Cohort{}
		for rows.Next() {
			var week string
			var size, offset, users int64
			if err := rows.Scan(&week, &size, &offset, &users); err != nil {
				return err
			}
			if n := len(result.Cohorts); n == 0 || result.Cohorts[n-1].Week != week {
				result.Cohorts = append(result.Cohorts, Cohort{Week: week, Size: size, Retained: make([]int64, cohortWeeks-1)})
			}
			if offset >= 1 && offset < cohortWeeks {
				result.Cohorts[len(result.Cohorts)-1].Retained[offset-1] = users
			}
		}
		return rows.Err()
	})

	if err := r.pool.SendBatch(ctx, batch).Close(); err != nil {
		return Overview{}, fmt.Errorf("analytics overview: %w", err)
	}
	return result, nil
}

func startOfWeek(day time.Time) time.Time {
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
	return day.AddDate(0, 0, -offset)
}

// Export datasets are pseudonymous: IDs and facts, never names, emails or
// phone numbers. {since} is replaced with a server-generated timestamp
// literal (COPY does not accept bind parameters).
var exportQueries = map[string]string{
	"users": `SELECT u.id, u.role, u.status, COALESCE(NULLIF(BTRIM(LOWER(u.source)), ''), 'direct') AS source,
			(u.referral_code IS NOT NULL) AS from_waitlist, u.referred_by_code,
			u.created_at, CASE WHEN u.status = 'REGISTERED' THEN COALESCE(u.terms_accepted_at, u.created_at) END AS registered_at,
			p.current_band, p.target_band, p.exam_type, p.exam_date
		FROM users u LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE u.created_at >= {since} ORDER BY u.created_at`,
	"attempts": `SELECT id, user_id, material_type, material_id, status, score, max_score, band, started_at, submitted_at,
			EXTRACT(EPOCH FROM submitted_at - started_at)::bigint AS duration_sec
		FROM attempts WHERE started_at >= {since} ORDER BY started_at`,
	"activity": `SELECT day, user_id, first_seen_at FROM user_activity_days
		WHERE first_seen_at >= {since} ORDER BY day, user_id`,
	"full_mocks": `SELECT id, user_id, mock_test_id, status, current_section, started_at, submitted_at
		FROM full_mock_sessions WHERE started_at >= {since} ORDER BY started_at`,
}

func ValidDataset(name string) bool {
	_, ok := exportQueries[name]
	return ok
}

// Export streams a dataset straight from PostgreSQL with COPY … TO STDOUT:
// no rows are buffered in the API, so large exports stay flat in memory.
func (r *Repository) Export(ctx context.Context, w io.Writer, dataset string, since time.Time) error {
	query, ok := exportQueries[dataset]
	if !ok {
		return fmt.Errorf("unknown dataset %q", dataset)
	}
	literal := "'" + since.UTC().Format("2006-01-02 15:04:05") + "+00'::timestamptz"
	sql := "COPY (" + strings.Replace(query, "{since}", literal, 1) + ") TO STDOUT WITH (FORMAT csv, HEADER true)"
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire export connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Conn().PgConn().CopyTo(ctx, w, sql); err != nil {
		return fmt.Errorf("export %s: %w", dataset, err)
	}
	return nil
}

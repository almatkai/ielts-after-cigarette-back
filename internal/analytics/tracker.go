package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the API image may ship without a zoneinfo database

	"github.com/almatkai/ielts-after-cigarette-back/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Product calendar: "today" and daily buckets follow Kazakhstan time.
const TimeZone = "Asia/Almaty"

var location = mustLoadLocation(TimeZone)

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

const (
	// OnlineWindow is how recently a heartbeat or API call must have happened
	// for a visitor to count as online. The web client pings every 30s.
	OnlineWindow = 2 * time.Minute

	keyOnlineUsers    = "iac:analytics:online:users"
	keyOnlineVisitors = "iac:analytics:online:visitors"
	keyVisitorPaths   = "iac:analytics:online:paths"
	keyPrefix         = "iac:analytics:"
	dayKeyTTL         = 100 * 24 * time.Hour
	userTouchInterval = 30 * time.Second
	maxPathLength     = 160
	maxLocalThrottle  = 50_000
)

// Tracker records presence in Redis and one activity row per user per day in
// PostgreSQL. Every write is best effort and off the request path: analytics
// must never slow down or fail a student's request.
type Tracker struct {
	redis  *redis.Client
	pool   *pgxpool.Pool
	logger *slog.Logger
	now    func() time.Time

	mu        sync.Mutex
	lastTouch map[uuid.UUID]time.Time
}

func NewTracker(redisClient *redis.Client, pool *pgxpool.Pool, logger *slog.Logger) *Tracker {
	return &Tracker{redis: redisClient, pool: pool, logger: logger, now: time.Now, lastTouch: make(map[uuid.UUID]time.Time)}
}

func dayKey(kind string, day time.Time) string {
	return keyPrefix + kind + ":" + day.In(location).Format("2006-01-02")
}

// Middleware marks the authenticated user as online. It runs after
// auth.Authenticate, so the user ID is always present when the request is
// authenticated.
func (t *Tracker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userID, ok := auth.UserID(r.Context()); ok && auth.Role(r.Context()) != "GUEST" {
			t.TouchUser(userID)
		}
		next.ServeHTTP(w, r)
	})
}

// TouchUser is throttled in process so a busy page that fires many API calls
// costs at most one Redis round trip per user every userTouchInterval.
func (t *Tracker) TouchUser(userID uuid.UUID) {
	if t == nil || t.redis == nil || userID == uuid.Nil {
		return
	}
	now := t.now()
	t.mu.Lock()
	if last, ok := t.lastTouch[userID]; ok && now.Sub(last) < userTouchInterval {
		t.mu.Unlock()
		return
	}
	if len(t.lastTouch) >= maxLocalThrottle {
		clear(t.lastTouch)
	}
	t.lastTouch[userID] = now
	t.mu.Unlock()

	go t.touchUser(userID, now)
}

func (t *Tracker) touchUser(userID uuid.UUID, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := userID.String()
	pipe := t.redis.Pipeline()
	pipe.ZAdd(ctx, keyOnlineUsers, redis.Z{Score: float64(now.Unix()), Member: id})
	pipe.ZRemRangeByScore(ctx, keyOnlineUsers, "-inf", strconv.FormatInt(now.Add(-2*OnlineWindow).Unix(), 10))
	firstToday := pipe.SetNX(ctx, dayKey("seen", now)+":"+id, 1, 26*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		t.warn("record user presence", err)
		return
	}
	if !firstToday.Val() || t.pool == nil {
		return
	}
	if _, err := t.pool.Exec(ctx, `
		INSERT INTO user_activity_days (day, user_id, first_seen_at)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, now.In(location).Format("2006-01-02"), userID, now); err != nil {
		// Let the next request retry the daily row.
		t.redis.Del(ctx, dayKey("seen", now)+":"+id)
		t.warn("record user activity day", err)
	}
}

// Ping records an anonymous (or signed-in) browser tab heartbeat. pageView is
// true when the tab navigated, so heartbeats do not inflate page views.
func (t *Tracker) Ping(ctx context.Context, visitorID uuid.UUID, path string, pageView bool) error {
	if t == nil || t.redis == nil {
		return nil
	}
	now := t.now()
	id := visitorID.String()
	path = normalizePath(path)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	pipe := t.redis.Pipeline()
	pipe.ZAdd(ctx, keyOnlineVisitors, redis.Z{Score: float64(now.Unix()), Member: id})
	pipe.HSet(ctx, keyVisitorPaths, id, path)
	visitorsKey := dayKey("visitors", now)
	pipe.PFAdd(ctx, visitorsKey, id)
	pipe.Expire(ctx, visitorsKey, dayKeyTTL)
	if pageView {
		pagesKey := dayKey("pages", now)
		viewsKey := dayKey("views", now)
		pipe.ZIncrBy(ctx, pagesKey, 1, path)
		pipe.Expire(ctx, pagesKey, dayKeyTTL)
		pipe.Incr(ctx, viewsKey)
		pipe.Expire(ctx, viewsKey, dayKeyTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("record visitor ping: %w", err)
	}
	// Amortised cleanup keeps the presence structures bounded even when
	// nobody opens the dashboard.
	if rand.IntN(50) == 0 {
		t.trim(ctx, now)
	}
	return nil
}

func (t *Tracker) trim(ctx context.Context, now time.Time) {
	cutoff := strconv.FormatInt(now.Add(-2*OnlineWindow).Unix(), 10)
	stale, err := t.redis.ZRangeByScore(ctx, keyOnlineVisitors, &redis.ZRangeBy{Min: "-inf", Max: cutoff}).Result()
	if err != nil {
		t.warn("list stale visitors", err)
		return
	}
	pipe := t.redis.Pipeline()
	if len(stale) > 0 {
		pipe.HDel(ctx, keyVisitorPaths, stale...)
	}
	pipe.ZRemRangeByScore(ctx, keyOnlineVisitors, "-inf", cutoff)
	pipe.ZRemRangeByScore(ctx, keyOnlineUsers, "-inf", cutoff)
	if _, err := pipe.Exec(ctx); err != nil {
		t.warn("trim presence", err)
	}
}

type PageCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

type Realtime struct {
	Available      bool        `json:"available"`
	OnlineUsers    int64       `json:"onlineUsers"`
	OnlineVisitors int64       `json:"onlineVisitors"`
	VisitorsToday  int64       `json:"visitorsToday"`
	PageViewsToday int64       `json:"pageViewsToday"`
	ActivePages    []PageCount `json:"activePages"`
	TopPagesToday  []PageCount `json:"topPagesToday"`
	WindowSeconds  int         `json:"windowSeconds"`
	GeneratedAt    time.Time   `json:"generatedAt"`
}

// Realtime reads only Redis: it is cheap enough to poll every few seconds.
func (t *Tracker) Realtime(ctx context.Context) (Realtime, error) {
	now := t.now()
	result := Realtime{ActivePages: []PageCount{}, TopPagesToday: []PageCount{}, WindowSeconds: int(OnlineWindow.Seconds()), GeneratedAt: now.UTC()}
	if t == nil || t.redis == nil {
		return result, nil
	}
	t.trim(ctx, now)
	since := strconv.FormatInt(now.Add(-OnlineWindow).Unix(), 10)

	pipe := t.redis.Pipeline()
	users := pipe.ZCount(ctx, keyOnlineUsers, since, "+inf")
	visitors := pipe.ZRangeByScore(ctx, keyOnlineVisitors, &redis.ZRangeBy{Min: since, Max: "+inf"})
	visitorsToday := pipe.PFCount(ctx, dayKey("visitors", now))
	views := pipe.Get(ctx, dayKey("views", now))
	topPages := pipe.ZRevRangeWithScores(ctx, dayKey("pages", now), 0, 9)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return result, fmt.Errorf("read realtime analytics: %w", err)
	}
	result.Available = true
	result.OnlineUsers = users.Val()
	result.OnlineVisitors = int64(len(visitors.Val()))
	result.VisitorsToday = visitorsToday.Val()
	result.PageViewsToday, _ = views.Int64()
	for _, z := range topPages.Val() {
		result.TopPagesToday = append(result.TopPagesToday, PageCount{Path: fmt.Sprint(z.Member), Count: int64(z.Score)})
	}

	if ids := visitors.Val(); len(ids) > 0 {
		paths, err := t.redis.HMGet(ctx, keyVisitorPaths, ids...).Result()
		if err != nil {
			return result, fmt.Errorf("read active pages: %w", err)
		}
		counts := map[string]int64{}
		for _, value := range paths {
			if path, ok := value.(string); ok {
				counts[path]++
			}
		}
		for path, count := range counts {
			result.ActivePages = append(result.ActivePages, PageCount{Path: path, Count: count})
		}
		sort.Slice(result.ActivePages, func(i, j int) bool {
			a, b := result.ActivePages[i], result.ActivePages[j]
			return a.Count > b.Count || (a.Count == b.Count && a.Path < b.Path)
		})
		if len(result.ActivePages) > 10 {
			result.ActivePages = result.ActivePages[:10]
		}
	}
	return result, nil
}

// DailyVisitors returns unique visitors (HyperLogLog, ~0.8% error) and page
// views per calendar day, oldest first.
func (t *Tracker) DailyVisitors(ctx context.Context, days []time.Time) (visitors, views []int64) {
	visitors, views = make([]int64, len(days)), make([]int64, len(days))
	if t == nil || t.redis == nil || len(days) == 0 {
		return visitors, views
	}
	pipe := t.redis.Pipeline()
	visitorCmds := make([]*redis.IntCmd, len(days))
	viewCmds := make([]*redis.StringCmd, len(days))
	for i, day := range days {
		visitorCmds[i] = pipe.PFCount(ctx, dayKey("visitors", day))
		viewCmds[i] = pipe.Get(ctx, dayKey("views", day))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		t.warn("read daily visitors", err)
	}
	for i := range days {
		visitors[i] = visitorCmds[i].Val()
		views[i], _ = viewCmds[i].Int64()
	}
	return visitors, views
}

func (t *Tracker) warn(operation string, err error) {
	if t.logger != nil && !errors.Is(err, context.Canceled) {
		t.logger.Warn("analytics: "+operation, "error", err)
	}
}

// normalizePath keeps only the route pattern: no query strings (they can
// carry tokens), IDs collapsed so /reading/<uuid> groups as one page.
func normalizePath(path string) string {
	for i, r := range path {
		if r == '?' || r == '#' {
			path = path[:i]
			break
		}
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, segment := range segments {
		if _, err := uuid.Parse(segment); err == nil {
			segments[i] = ":id"
		} else if _, err := strconv.Atoi(segment); err == nil {
			segments[i] = ":n"
		}
	}
	path = "/" + strings.Join(segments, "/")
	if len(path) > maxPathLength {
		path = path[:maxPathLength]
	}
	return path
}

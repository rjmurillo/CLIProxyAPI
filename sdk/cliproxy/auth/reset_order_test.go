package auth

import (
	"context"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func unixString(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }

func claudeAuth(id string, weekly, fiveHour time.Time) *Auth {
	signals := map[string]string{}
	if !weekly.IsZero() {
		signals["Anthropic-Ratelimit-Unified-7d-Reset"] = unixString(weekly)
	}
	if !fiveHour.IsZero() {
		signals["Anthropic-Ratelimit-Unified-5h-Reset"] = unixString(fiveHour)
	}
	return &Auth{ID: id, Provider: "claude", Quota: QuotaState{Signals: signals}}
}

func TestAuthQuotaResetAt(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	tomorrow := now.Add(24 * time.Hour)
	inFiveHours := now.Add(5 * time.Hour)

	cases := []struct {
		name string
		auth *Auth
		want time.Time
	}{
		{"nil auth", nil, time.Time{}},
		{"no signals", &Auth{ID: "x"}, time.Time{}},
		{"claude weekly wins over five hour", claudeAuth("c", tomorrow, inFiveHours), tomorrow},
		{"claude five hour fallback", claudeAuth("c", time.Time{}, inFiveHours), inFiveHours},
		{"past weekly falls back to five hour", claudeAuth("c", now.Add(-time.Hour), inFiveHours), inFiveHours},
		{"all past is unknown", claudeAuth("c", now.Add(-time.Hour), now.Add(-time.Minute)), time.Time{}},
		{
			"codex secondary absolute",
			&Auth{ID: "x", Quota: QuotaState{Signals: map[string]string{
				"X-Codex-Secondary-Reset-At": unixString(tomorrow),
				"X-Codex-Primary-Reset-At":   unixString(inFiveHours),
			}}},
			tomorrow,
		},
		{
			"codex relative seconds from observed time",
			&Auth{ID: "x", Quota: QuotaState{
				ObservedAt: now,
				Signals:    map[string]string{"x-codex-secondary-reset-after-seconds": "3600"},
			}},
			now.Add(time.Hour),
		},
		{
			"garbage values are ignored",
			&Auth{ID: "x", Quota: QuotaState{Signals: map[string]string{
				"anthropic-ratelimit-unified-7d-reset": "soon",
			}}},
			time.Time{},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := authQuotaResetAt(tc.auth, now); !got.Equal(tc.want) {
				t.Fatalf("authQuotaResetAt() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFillFirstSelectorPick_SoonestResetFirst(t *testing.T) {
	t.Parallel()
	now := time.Now()
	auths := []*Auth{
		claudeAuth("a-four-days", now.Add(96*time.Hour), time.Time{}),
		claudeAuth("b-tomorrow", now.Add(24*time.Hour), time.Time{}),
		{ID: "c-unknown", Provider: "claude"},
	}
	got, err := (&FillFirstSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "b-tomorrow" {
		t.Fatalf("Pick() auth.ID = %q, want b-tomorrow", got.ID)
	}
}

func TestFillFirstSelectorPick_UnknownResetsKeepIDOrder(t *testing.T) {
	t.Parallel()
	auths := []*Auth{{ID: "b"}, {ID: "a"}, {ID: "c"}}
	got, err := (&FillFirstSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want a", got.ID)
	}
}

func TestFillFirstSelectorPick_ExpiredResetSortsLast(t *testing.T) {
	t.Parallel()
	now := time.Now()
	auths := []*Auth{
		claudeAuth("a-rolled-over", now.Add(-time.Hour), time.Time{}),
		claudeAuth("b-next-week", now.Add(160*time.Hour), time.Time{}),
	}
	got, err := (&FillFirstSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "b-next-week" {
		t.Fatalf("Pick() auth.ID = %q, want b-next-week", got.ID)
	}
}

func TestSchedulerPick_FillFirstSoonestResetFirst(t *testing.T) {
	t.Parallel()
	now := time.Now()
	scheduler := newSchedulerForTest(
		&FillFirstSelector{},
		claudeAuth("a-four-days", now.Add(96*time.Hour), time.Time{}),
		claudeAuth("b-tomorrow", now.Add(24*time.Hour), time.Time{}),
		&Auth{ID: "c-unknown", Provider: "claude"},
	)
	for index := 0; index < 3; index++ {
		got, errPick := scheduler.pickSingle(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
		if errPick != nil {
			t.Fatalf("pickSingle() #%d error = %v", index, errPick)
		}
		if got == nil || got.ID != "b-tomorrow" {
			t.Fatalf("pickSingle() #%d auth = %#v, want b-tomorrow", index, got)
		}
	}
}

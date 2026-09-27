//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	recentReleases []*GitHubRelease
	recentErr      error
	latestErr      error
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(context.Context, string) (*GitHubRelease, error) {
	return s.release, s.latestErr
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error) {
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestCompareVersionsBuildMetadata(t *testing.T) {
	for _, tc := range []struct {
		current string
		latest  string
		want    int
	}{
		{"0.2.8+mainstation.1", "0.2.8", 0},
		{"v0.2.8+mainstation.2", "v0.2.8+other.1", 0},
		{"0.2.8+mainstation.1", "0.2.9", -1},
		{"0.2.8+mainstation.1", "0.2.7", 1},
		{"0.2.9", "0.2.8+mainstation.1", 1},
		{"0.2.8-rc.1+build.2", "0.2.8", 0},
	} {
		t.Run(tc.current+"/"+tc.latest, func(t *testing.T) {
			require.Equal(t, tc.want, compareVersions(tc.current, tc.latest))
		})
	}
}

func TestUpdateServiceCheckUpdateReportsPolicy(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, mode := range []string{"fresh", "cached", "cached-error", "error"} {
			t.Run(fmt.Sprintf("disabled=%t/%s", disabled, mode), func(t *testing.T) {
				t.Setenv("UPDATE_DISABLED", strconv.FormatBool(disabled))
				client := &updateServiceGitHubClientStub{release: &GitHubRelease{TagName: "v0.2.8"}}
				cache := &updateServiceCacheStub{}
				svc := NewUpdateService(cache, client, "0.2.8+mainstation.2", "release")
				if mode == "cached" || mode == "cached-error" {
					_, err := svc.CheckUpdate(context.Background(), true)
					require.NoError(t, err)
				}
				if mode == "error" || mode == "cached-error" {
					client.latestErr = errors.New("upstream unavailable")
				}
				info, err := svc.CheckUpdate(context.Background(), mode != "cached")
				require.NoError(t, err)
				require.Equal(t, disabled, info.UpdatesDisabled)
				require.False(t, info.HasUpdate)
				require.Equal(t, mode == "cached" || mode == "cached-error", info.Cached)
				require.Equal(t, "release", info.BuildType)
			})
		}
	}
}

func TestUpdateServiceDisabledBlocksBinaryChanges(t *testing.T) {
	for _, value := range []string{"true", "1", " TRUE "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("UPDATE_DISABLED", value)
			svc := NewUpdateService(nil, nil, "0.2.8", "release")
			require.ErrorIs(t, svc.PerformUpdate(context.Background()), ErrUpdatesDisabled)
			require.ErrorIs(t, svc.Rollback(), ErrUpdatesDisabled)
			require.ErrorIs(t, svc.RollbackToVersion(context.Background(), "0.2.7"), ErrUpdatesDisabled)
		})
	}
}

func TestUpdateServiceDisabledPreservesUpdateChecks(t *testing.T) {
	t.Setenv("UPDATE_DISABLED", "true")
	svc := NewUpdateService(&updateServiceCacheStub{}, &updateServiceGitHubClientStub{
		release: &GitHubRelease{TagName: "v0.2.9", Name: "v0.2.9"},
	}, "0.2.8", "release")
	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.True(t, info.HasUpdate)
	require.True(t, info.UpdatesDisabled)
	require.Equal(t, "0.2.9", info.LatestVersion)
}

func TestUpdateServiceDisabledDefaultsOff(t *testing.T) {
	for _, value := range []string{"", "false", "0"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("UPDATE_DISABLED", value)
			svc := NewUpdateService(nil, nil, "0.2.8", "release")
			require.False(t, svc.updatesDisabled)
		})
	}
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}

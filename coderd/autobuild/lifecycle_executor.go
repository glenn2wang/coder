package autobuild

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"

	"cdr.dev/slog"
	"github.com/coder/coder/coderd/database"
	"github.com/coder/coder/coderd/database/db2sdk"
	"github.com/coder/coder/coderd/database/dbauthz"
	"github.com/coder/coder/coderd/schedule"
	"github.com/coder/coder/coderd/wsbuilder"
	"github.com/coder/coder/codersdk"
)

// Executor automatically starts or stops workspaces.
type Executor struct {
	ctx                   context.Context
	db                    database.Store
	templateScheduleStore *atomic.Pointer[schedule.TemplateScheduleStore]
	log                   slog.Logger
	tick                  <-chan time.Time
	statsCh               chan<- Stats
}

// Stats contains information about one run of Executor.
type Stats struct {
	Transitions map[uuid.UUID]database.WorkspaceTransition
	Elapsed     time.Duration
	Error       error
}

// New returns a new wsactions executor.
func NewExecutor(ctx context.Context, db database.Store, tss *atomic.Pointer[schedule.TemplateScheduleStore], log slog.Logger, tick <-chan time.Time) *Executor {
	le := &Executor{
		//nolint:gocritic // Autostart has a limited set of permissions.
		ctx:                   dbauthz.AsAutostart(ctx),
		db:                    db,
		templateScheduleStore: tss,
		tick:                  tick,
		log:                   log.Named("autobuild"),
	}
	return le
}

// WithStatsChannel will cause Executor to push a RunStats to ch after
// every tick.
func (e *Executor) WithStatsChannel(ch chan<- Stats) *Executor {
	e.statsCh = ch
	return e
}

// Run will cause executor to start or stop workspaces on every
// tick from its channel. It will stop when its context is Done, or when
// its channel is closed.
func (e *Executor) Run() {
	go func() {
		for {
			select {
			case <-e.ctx.Done():
				return
			case t, ok := <-e.tick:
				if !ok {
					return
				}
				stats := e.runOnce(t)
				if stats.Error != nil {
					e.log.Error(e.ctx, "error running once", slog.Error(stats.Error))
				}
				if e.statsCh != nil {
					select {
					case <-e.ctx.Done():
						return
					case e.statsCh <- stats:
					}
				}
				e.log.Debug(e.ctx, "run stats", slog.F("elapsed", stats.Elapsed), slog.F("transitions", stats.Transitions))
			}
		}
	}()
}

func (e *Executor) runOnce(t time.Time) Stats {
	var err error
	stats := Stats{
		Transitions: make(map[uuid.UUID]database.WorkspaceTransition),
	}
	// we build the map of transitions concurrently, so need a mutex to serialize writes to the map
	statsMu := sync.Mutex{}
	defer func() {
		stats.Elapsed = time.Since(t)
		stats.Error = err
	}()
	currentTick := t.Truncate(time.Minute)

	// TTL is set at the workspace level, and deadline at the workspace build level.
	// When a workspace build is created, its deadline initially starts at zero.
	// When provisionerd successfully completes a provision job, the deadline is
	// set to now + TTL if the associated workspace has a TTL set. This deadline
	// is what we compare against when performing autostop operations, rounded down
	// to the minute.
	//
	// NOTE: If a workspace build is created with a given TTL and then the user either
	//       changes or unsets the TTL, the deadline for the workspace build will not
	//       have changed. This behavior is as expected per #2229.
	workspaces, err := e.db.GetWorkspacesEligibleForTransition(e.ctx, t)
	if err != nil {
		e.log.Error(e.ctx, "get workspaces for autostart or autostop", slog.Error(err))
		return stats
	}

	// We only use errgroup here for convenience of API, not for early
	// cancellation. This means we only return nil errors in th eg.Go.
	eg := errgroup.Group{}
	// Limit the concurrency to avoid overloading the database.
	eg.SetLimit(10)

	for _, ws := range workspaces {
		wsID := ws.ID
		log := e.log.With(slog.F("workspace_id", wsID))

		eg.Go(func() error {
			err := e.db.InTx(func(tx database.Store) error {
				// Re-check eligibility since the first check was outside the
				// transaction and the workspace settings may have changed.
				ws, err := tx.GetWorkspaceByID(e.ctx, wsID)
				if err != nil {
					log.Error(e.ctx, "get workspace autostart failed", slog.Error(err))
					return nil
				}

				// Determine the workspace state based on its latest build.
				latestBuild, err := tx.GetLatestWorkspaceBuildByWorkspaceID(e.ctx, ws.ID)
				if err != nil {
					log.Warn(e.ctx, "get latest workspace build", slog.Error(err))
					return nil
				}
				templateSchedule, err := (*(e.templateScheduleStore.Load())).Get(e.ctx, tx, ws.TemplateID)
				if err != nil {
					log.Warn(e.ctx, "get template schedule options", slog.Error(err))
					return nil
				}

				latestJob, err := tx.GetProvisionerJobByID(e.ctx, latestBuild.JobID)
				if err != nil {
					log.Warn(e.ctx, "get last provisioner job for workspace %q: %w", slog.Error(err))
					return nil
				}

				nextTransition, reason, err := getNextTransition(ws, latestBuild, latestJob, templateSchedule, currentTick)
				if err != nil {
					log.Debug(e.ctx, "skipping workspace", slog.Error(err))
					return nil
				}

				if nextTransition != "" {
					builder := wsbuilder.New(ws, nextTransition).
						SetLastWorkspaceBuildInTx(&latestBuild).
						SetLastWorkspaceBuildJobInTx(&latestJob).
						Reason(reason)

					if _, _, err := builder.Build(e.ctx, tx, nil); err != nil {
						log.Error(e.ctx, "unable to transition workspace",
							slog.F("transition", nextTransition),
							slog.Error(err),
						)
						return nil
					}
				}

				// Lock the workspace if it has breached the template's
				// threshold for inactivity.
				if reason == database.BuildReasonAutolock {
					ws, err = tx.UpdateWorkspaceLockedDeletingAt(e.ctx, database.UpdateWorkspaceLockedDeletingAtParams{
						ID: ws.ID,
						LockedAt: sql.NullTime{
							Time:  database.Now(),
							Valid: true,
						},
					})
					if err != nil {
						log.Error(e.ctx, "unable to lock workspace",
							slog.F("transition", nextTransition),
							slog.Error(err),
						)
						return nil
					}

					log.Info(e.ctx, "locked workspace",
						slog.F("last_used_at", ws.LastUsedAt),
						slog.F("inactivity_ttl", templateSchedule.InactivityTTL),
						slog.F("since_last_used_at", time.Since(ws.LastUsedAt)),
					)
				}

				if reason == database.BuildReasonAutodelete {
					log.Info(e.ctx, "deleted workspace",
						slog.F("locked_at", ws.LockedAt.Time),
						slog.F("locked_ttl", templateSchedule.LockedTTL),
					)
				}

				if nextTransition == "" {
					return nil
				}

				statsMu.Lock()
				stats.Transitions[ws.ID] = nextTransition
				statsMu.Unlock()

				log.Info(e.ctx, "scheduling workspace transition",
					slog.F("transition", nextTransition),
					slog.F("reason", reason),
				)

				return nil

				// Run with RepeatableRead isolation so that the build process sees the same data
				// as our calculation that determines whether an autobuild is necessary.
			}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				log.Error(e.ctx, "workspace scheduling failed", slog.Error(err))
			}
			return nil
		})
	}

	// This should not happen since we don't want early cancellation.
	err = eg.Wait()
	if err != nil {
		e.log.Error(e.ctx, "workspace scheduling errgroup failed", slog.Error(err))
	}

	return stats
}

// getNextTransition returns the next eligible transition for the workspace
// as well as the reason for why it is transitioning. It is possible
// for this function to return a nil error as well as an empty transition.
// In such cases it means no provisioning should occur but the workspace
// may be "transitioning" to a new state (such as an inactive, stopped
// workspace transitioning to the locked state).
func getNextTransition(
	ws database.Workspace,
	latestBuild database.WorkspaceBuild,
	latestJob database.ProvisionerJob,
	templateSchedule schedule.TemplateScheduleOptions,
	currentTick time.Time,
) (
	database.WorkspaceTransition,
	database.BuildReason,
	error,
) {
	switch {
	case isEligibleForAutostop(ws, latestBuild, latestJob, currentTick):
		return database.WorkspaceTransitionStop, database.BuildReasonAutostop, nil
	case isEligibleForAutostart(ws, latestBuild, latestJob, templateSchedule, currentTick):
		return database.WorkspaceTransitionStart, database.BuildReasonAutostart, nil
	case isEligibleForFailedStop(latestBuild, latestJob, templateSchedule, currentTick):
		return database.WorkspaceTransitionStop, database.BuildReasonAutostop, nil
	case isEligibleForLockedStop(ws, templateSchedule, currentTick):
		// Only stop started workspaces.
		if latestBuild.Transition == database.WorkspaceTransitionStart {
			return database.WorkspaceTransitionStop, database.BuildReasonAutolock, nil
		}
		// We shouldn't transition the workspace but we should still
		// lock it.
		return "", database.BuildReasonAutolock, nil

	case isEligibleForDelete(ws, templateSchedule, currentTick):
		return database.WorkspaceTransitionDelete, database.BuildReasonAutodelete, nil
	default:
		return "", "", xerrors.Errorf("last transition not valid for autostart or autostop")
	}
}

// isEligibleForAutostart returns true if the workspace should be autostarted.
func isEligibleForAutostart(ws database.Workspace, build database.WorkspaceBuild, job database.ProvisionerJob, templateSchedule schedule.TemplateScheduleOptions, currentTick time.Time) bool {
	// Don't attempt to autostart failed workspaces.
	if db2sdk.ProvisionerJobStatus(job) == codersdk.ProvisionerJobFailed {
		return false
	}

	// If the workspace is locked we should not autostart it.
	if ws.LockedAt.Valid {
		return false
	}

	// If the last transition for the workspace was not 'stop' then the workspace
	// cannot be started.
	if build.Transition != database.WorkspaceTransitionStop {
		return false
	}

	// If autostart isn't enabled, or the schedule isn't valid/populated we can't
	// autostart the workspace.
	if !templateSchedule.UserAutostartEnabled || !ws.AutostartSchedule.Valid || ws.AutostartSchedule.String == "" {
		return false
	}

	sched, err := schedule.Weekly(ws.AutostartSchedule.String)
	if err != nil {
		return false
	}
	// Round down to the nearest minute, as this is the finest granularity cron supports.
	// Truncate is probably not necessary here, but doing it anyway to be sure.
	nextTransition := sched.Next(build.CreatedAt).Truncate(time.Minute)

	return !currentTick.Before(nextTransition)
}

// isEligibleForAutostart returns true if the workspace should be autostopped.
func isEligibleForAutostop(ws database.Workspace, build database.WorkspaceBuild, job database.ProvisionerJob, currentTick time.Time) bool {
	if db2sdk.ProvisionerJobStatus(job) == codersdk.ProvisionerJobFailed {
		return false
	}

	// If the workspace is locked we should not autostop it.
	if ws.LockedAt.Valid {
		return false
	}

	// A workspace must be started in order for it to be auto-stopped.
	return build.Transition == database.WorkspaceTransitionStart &&
		!build.Deadline.IsZero() &&
		// We do not want to stop a workspace prior to it breaching its deadline.
		!currentTick.Before(build.Deadline)
}

// isEligibleForLockedStop returns true if the workspace should be locked
// for breaching the inactivity threshold of the template.
func isEligibleForLockedStop(ws database.Workspace, templateSchedule schedule.TemplateScheduleOptions, currentTick time.Time) bool {
	// Only attempt to lock workspaces not already locked.
	return !ws.LockedAt.Valid &&
		// The template must specify an inactivity TTL.
		templateSchedule.InactivityTTL > 0 &&
		// The workspace must breach the inactivity TTL.
		currentTick.Sub(ws.LastUsedAt) > templateSchedule.InactivityTTL
}

func isEligibleForDelete(ws database.Workspace, templateSchedule schedule.TemplateScheduleOptions, currentTick time.Time) bool {
	// Only attempt to delete locked workspaces.
	return ws.LockedAt.Valid && ws.DeletingAt.Valid &&
		// Locked workspaces should only be deleted if a locked_ttl is specified.
		templateSchedule.LockedTTL > 0 &&
		// The workspace must breach the locked_ttl.
		currentTick.After(ws.DeletingAt.Time)
}

// isEligibleForFailedStop returns true if the workspace is eligible to be stopped
// due to a failed build.
func isEligibleForFailedStop(build database.WorkspaceBuild, job database.ProvisionerJob, templateSchedule schedule.TemplateScheduleOptions, currentTick time.Time) bool {
	// If the template has specified a failure TLL.
	return templateSchedule.FailureTTL > 0 &&
		// And the job resulted in failure.
		db2sdk.ProvisionerJobStatus(job) == codersdk.ProvisionerJobFailed &&
		build.Transition == database.WorkspaceTransitionStart &&
		// And sufficient time has elapsed since the job has completed.
		job.CompletedAt.Valid &&
		currentTick.Sub(job.CompletedAt.Time) > templateSchedule.FailureTTL
}

package dbfake

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"golang.org/x/xerrors"

	"github.com/coder/coder/coderd/database"
	"github.com/coder/coder/coderd/database/db2sdk"
	"github.com/coder/coder/coderd/httpapi"
	"github.com/coder/coder/coderd/rbac"
	"github.com/coder/coder/coderd/rbac/regosql"
	"github.com/coder/coder/coderd/util/slice"
	"github.com/coder/coder/codersdk"
)

var validProxyByHostnameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

var errDuplicateKey = &pq.Error{
	Code:    "23505",
	Message: "duplicate key value violates unique constraint",
}

// New returns an in-memory fake of the database.
func New() database.Store {
	q := &FakeQuerier{
		mutex: &sync.RWMutex{},
		data: &data{
			apiKeys:                   make([]database.APIKey, 0),
			organizationMembers:       make([]database.OrganizationMember, 0),
			organizations:             make([]database.Organization, 0),
			users:                     make([]database.User, 0),
			gitAuthLinks:              make([]database.GitAuthLink, 0),
			groups:                    make([]database.Group, 0),
			groupMembers:              make([]database.GroupMember, 0),
			auditLogs:                 make([]database.AuditLog, 0),
			files:                     make([]database.File, 0),
			gitSSHKey:                 make([]database.GitSSHKey, 0),
			parameterSchemas:          make([]database.ParameterSchema, 0),
			provisionerDaemons:        make([]database.ProvisionerDaemon, 0),
			workspaceAgents:           make([]database.WorkspaceAgent, 0),
			provisionerJobLogs:        make([]database.ProvisionerJobLog, 0),
			workspaceResources:        make([]database.WorkspaceResource, 0),
			workspaceResourceMetadata: make([]database.WorkspaceResourceMetadatum, 0),
			provisionerJobs:           make([]database.ProvisionerJob, 0),
			templateVersions:          make([]database.TemplateVersionTable, 0),
			templates:                 make([]database.TemplateTable, 0),
			workspaceAgentStats:       make([]database.WorkspaceAgentStat, 0),
			workspaceAgentLogs:        make([]database.WorkspaceAgentLog, 0),
			workspaceBuilds:           make([]database.WorkspaceBuildTable, 0),
			workspaceApps:             make([]database.WorkspaceApp, 0),
			workspaces:                make([]database.Workspace, 0),
			licenses:                  make([]database.License, 0),
			workspaceProxies:          make([]database.WorkspaceProxy, 0),
			locks:                     map[int64]struct{}{},
		},
	}
	q.defaultProxyDisplayName = "Default"
	q.defaultProxyIconURL = "/emojis/1f3e1.png"
	return q
}

type rwMutex interface {
	Lock()
	RLock()
	Unlock()
	RUnlock()
}

// inTxMutex is a no op, since inside a transaction we are already locked.
type inTxMutex struct{}

func (inTxMutex) Lock()    {}
func (inTxMutex) RLock()   {}
func (inTxMutex) Unlock()  {}
func (inTxMutex) RUnlock() {}

// FakeQuerier replicates database functionality to enable quick testing.  It's an exported type so that our test code
// can do type checks.
type FakeQuerier struct {
	mutex rwMutex
	*data
}

func (*FakeQuerier) Wrappers() []string {
	return []string{}
}

type fakeTx struct {
	*FakeQuerier
	locks map[int64]struct{}
}

type data struct {
	// Legacy tables
	apiKeys             []database.APIKey
	organizations       []database.Organization
	organizationMembers []database.OrganizationMember
	users               []database.User
	userLinks           []database.UserLink

	// New tables
	workspaceAgentStats       []database.WorkspaceAgentStat
	auditLogs                 []database.AuditLog
	files                     []database.File
	gitAuthLinks              []database.GitAuthLink
	gitSSHKey                 []database.GitSSHKey
	groupMembers              []database.GroupMember
	groups                    []database.Group
	licenses                  []database.License
	parameterSchemas          []database.ParameterSchema
	provisionerDaemons        []database.ProvisionerDaemon
	provisionerJobLogs        []database.ProvisionerJobLog
	provisionerJobs           []database.ProvisionerJob
	replicas                  []database.Replica
	templateVersions          []database.TemplateVersionTable
	templateVersionParameters []database.TemplateVersionParameter
	templateVersionVariables  []database.TemplateVersionVariable
	templates                 []database.TemplateTable
	workspaceAgents           []database.WorkspaceAgent
	workspaceAgentMetadata    []database.WorkspaceAgentMetadatum
	workspaceAgentLogs        []database.WorkspaceAgentLog
	workspaceApps             []database.WorkspaceApp
	workspaceBuilds           []database.WorkspaceBuildTable
	workspaceBuildParameters  []database.WorkspaceBuildParameter
	workspaceResourceMetadata []database.WorkspaceResourceMetadatum
	workspaceResources        []database.WorkspaceResource
	workspaces                []database.Workspace
	workspaceProxies          []database.WorkspaceProxy
	// Locks is a map of lock names. Any keys within the map are currently
	// locked.
	locks                   map[int64]struct{}
	deploymentID            string
	derpMeshKey             string
	lastUpdateCheck         []byte
	serviceBanner           []byte
	logoURL                 string
	appSecurityKey          string
	oauthSigningKey         string
	lastLicenseID           int32
	defaultProxyDisplayName string
	defaultProxyIconURL     string
}

func validateDatabaseTypeWithValid(v reflect.Value) (handled bool, err error) {
	if v.Kind() == reflect.Struct {
		return false, nil
	}

	if v.CanInterface() {
		if !strings.Contains(v.Type().PkgPath(), "coderd/database") {
			return true, nil
		}
		if valid, ok := v.Interface().(interface{ Valid() bool }); ok {
			if !valid.Valid() {
				return true, xerrors.Errorf("invalid %s: %q", v.Type().Name(), v.Interface())
			}
		}
		return true, nil
	}
	return false, nil
}

// validateDatabaseType uses reflect to check if struct properties are types
// with a Valid() bool function set. If so, call it and return an error
// if false.
//
// Note that we only check immediate values and struct fields. We do not
// recurse into nested structs.
func validateDatabaseType(args interface{}) error {
	v := reflect.ValueOf(args)

	// Note: database.Null* types don't have a Valid method, we skip them here
	// because their embedded types may have a Valid method and we don't want
	// to bother with checking both that the Valid field is true and that the
	// type it embeds validates to true. We would need to check:
	//
	//	dbNullEnum.Valid && dbNullEnum.Enum.Valid()
	if strings.HasPrefix(v.Type().Name(), "Null") {
		return nil
	}

	if ok, err := validateDatabaseTypeWithValid(v); ok {
		return err
	}
	switch v.Kind() {
	case reflect.Struct:
		var errs []string
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if ok, err := validateDatabaseTypeWithValid(field); ok && err != nil {
				errs = append(errs, fmt.Sprintf("%s.%s: %s", v.Type().Name(), v.Type().Field(i).Name, err.Error()))
			}
		}
		if len(errs) > 0 {
			return xerrors.Errorf("invalid database type fields:\n\t%s", strings.Join(errs, "\n\t"))
		}
	default:
		panic(fmt.Sprintf("unhandled type: %s", v.Type().Name()))
	}
	return nil
}

func (*FakeQuerier) Ping(_ context.Context) (time.Duration, error) {
	return 0, nil
}

func (tx *fakeTx) AcquireLock(_ context.Context, id int64) error {
	if _, ok := tx.FakeQuerier.locks[id]; ok {
		return xerrors.Errorf("cannot acquire lock %d: already held", id)
	}
	tx.FakeQuerier.locks[id] = struct{}{}
	tx.locks[id] = struct{}{}
	return nil
}

func (tx *fakeTx) TryAcquireLock(_ context.Context, id int64) (bool, error) {
	if _, ok := tx.FakeQuerier.locks[id]; ok {
		return false, nil
	}
	tx.FakeQuerier.locks[id] = struct{}{}
	tx.locks[id] = struct{}{}
	return true, nil
}

func (tx *fakeTx) releaseLocks() {
	for id := range tx.locks {
		delete(tx.FakeQuerier.locks, id)
	}
	tx.locks = map[int64]struct{}{}
}

// InTx doesn't rollback data properly for in-memory yet.
func (q *FakeQuerier) InTx(fn func(database.Store) error, _ *sql.TxOptions) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	tx := &fakeTx{
		FakeQuerier: &FakeQuerier{mutex: inTxMutex{}, data: q.data},
		locks:       map[int64]struct{}{},
	}
	defer tx.releaseLocks()

	return fn(tx)
}

// getUserByIDNoLock is used by other functions in the database fake.
func (q *FakeQuerier) getUserByIDNoLock(id uuid.UUID) (database.User, error) {
	for _, user := range q.users {
		if user.ID == id {
			return user, nil
		}
	}
	return database.User{}, sql.ErrNoRows
}

func convertUsers(users []database.User, count int64) []database.GetUsersRow {
	rows := make([]database.GetUsersRow, len(users))
	for i, u := range users {
		rows[i] = database.GetUsersRow{
			ID:             u.ID,
			Email:          u.Email,
			Username:       u.Username,
			HashedPassword: u.HashedPassword,
			CreatedAt:      u.CreatedAt,
			UpdatedAt:      u.UpdatedAt,
			Status:         u.Status,
			RBACRoles:      u.RBACRoles,
			LoginType:      u.LoginType,
			AvatarURL:      u.AvatarURL,
			Deleted:        u.Deleted,
			LastSeenAt:     u.LastSeenAt,
			Count:          count,
		}
	}

	return rows
}

// mapAgentStatus determines the agent status based on different timestamps like created_at, last_connected_at, disconnected_at, etc.
// The function must be in sync with: coderd/workspaceagents.go:convertWorkspaceAgent.
func mapAgentStatus(dbAgent database.WorkspaceAgent, agentInactiveDisconnectTimeoutSeconds int64) string {
	var status string
	connectionTimeout := time.Duration(dbAgent.ConnectionTimeoutSeconds) * time.Second
	switch {
	case !dbAgent.FirstConnectedAt.Valid:
		switch {
		case connectionTimeout > 0 && database.Now().Sub(dbAgent.CreatedAt) > connectionTimeout:
			// If the agent took too long to connect the first time,
			// mark it as timed out.
			status = "timeout"
		default:
			// If the agent never connected, it's waiting for the compute
			// to start up.
			status = "connecting"
		}
	case dbAgent.DisconnectedAt.Time.After(dbAgent.LastConnectedAt.Time):
		// If we've disconnected after our last connection, we know the
		// agent is no longer connected.
		status = "disconnected"
	case database.Now().Sub(dbAgent.LastConnectedAt.Time) > time.Duration(agentInactiveDisconnectTimeoutSeconds)*time.Second:
		// The connection died without updating the last connected.
		status = "disconnected"
	case dbAgent.LastConnectedAt.Valid:
		// The agent should be assumed connected if it's under inactivity timeouts
		// and last connected at has been properly set.
		status = "connected"
	default:
		panic("unknown agent status: " + status)
	}
	return status
}

func (q *FakeQuerier) convertToWorkspaceRowsNoLock(ctx context.Context, workspaces []database.Workspace, count int64) []database.GetWorkspacesRow {
	rows := make([]database.GetWorkspacesRow, 0, len(workspaces))
	for _, w := range workspaces {
		wr := database.GetWorkspacesRow{
			ID:                w.ID,
			CreatedAt:         w.CreatedAt,
			UpdatedAt:         w.UpdatedAt,
			OwnerID:           w.OwnerID,
			OrganizationID:    w.OrganizationID,
			TemplateID:        w.TemplateID,
			Deleted:           w.Deleted,
			Name:              w.Name,
			AutostartSchedule: w.AutostartSchedule,
			Ttl:               w.Ttl,
			LastUsedAt:        w.LastUsedAt,
			LockedAt:          w.LockedAt,
			DeletingAt:        w.DeletingAt,
			Count:             count,
		}

		for _, t := range q.templates {
			if t.ID == w.TemplateID {
				wr.TemplateName = t.Name
				break
			}
		}

		if build, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, w.ID); err == nil {
			for _, tv := range q.templateVersions {
				if tv.ID == build.TemplateVersionID {
					wr.TemplateVersionID = tv.ID
					wr.TemplateVersionName = sql.NullString{
						Valid:  true,
						String: tv.Name,
					}
					break
				}
			}
		}

		rows = append(rows, wr)
	}
	return rows
}

func (q *FakeQuerier) getWorkspaceByIDNoLock(_ context.Context, id uuid.UUID) (database.Workspace, error) {
	for _, workspace := range q.workspaces {
		if workspace.ID == id {
			return workspace, nil
		}
	}
	return database.Workspace{}, sql.ErrNoRows
}

func (q *FakeQuerier) getWorkspaceByAgentIDNoLock(_ context.Context, agentID uuid.UUID) (database.Workspace, error) {
	var agent database.WorkspaceAgent
	for _, _agent := range q.workspaceAgents {
		if _agent.ID == agentID {
			agent = _agent
			break
		}
	}
	if agent.ID == uuid.Nil {
		return database.Workspace{}, sql.ErrNoRows
	}

	var resource database.WorkspaceResource
	for _, _resource := range q.workspaceResources {
		if _resource.ID == agent.ResourceID {
			resource = _resource
			break
		}
	}
	if resource.ID == uuid.Nil {
		return database.Workspace{}, sql.ErrNoRows
	}

	var build database.WorkspaceBuild
	for _, _build := range q.workspaceBuilds {
		if _build.JobID == resource.JobID {
			build = q.workspaceBuildWithUserNoLock(_build)
			break
		}
	}
	if build.ID == uuid.Nil {
		return database.Workspace{}, sql.ErrNoRows
	}

	for _, workspace := range q.workspaces {
		if workspace.ID == build.WorkspaceID {
			return workspace, nil
		}
	}

	return database.Workspace{}, sql.ErrNoRows
}

func (q *FakeQuerier) getWorkspaceBuildByIDNoLock(_ context.Context, id uuid.UUID) (database.WorkspaceBuild, error) {
	for _, build := range q.workspaceBuilds {
		if build.ID == id {
			return q.workspaceBuildWithUserNoLock(build), nil
		}
	}
	return database.WorkspaceBuild{}, sql.ErrNoRows
}

func (q *FakeQuerier) getLatestWorkspaceBuildByWorkspaceIDNoLock(_ context.Context, workspaceID uuid.UUID) (database.WorkspaceBuild, error) {
	var row database.WorkspaceBuild
	var buildNum int32 = -1
	for _, workspaceBuild := range q.workspaceBuilds {
		if workspaceBuild.WorkspaceID == workspaceID && workspaceBuild.BuildNumber > buildNum {
			row = q.workspaceBuildWithUserNoLock(workspaceBuild)
			buildNum = workspaceBuild.BuildNumber
		}
	}
	if buildNum == -1 {
		return database.WorkspaceBuild{}, sql.ErrNoRows
	}
	return row, nil
}

func (q *FakeQuerier) getTemplateByIDNoLock(_ context.Context, id uuid.UUID) (database.Template, error) {
	for _, template := range q.templates {
		if template.ID == id {
			return q.templateWithUserNoLock(template), nil
		}
	}
	return database.Template{}, sql.ErrNoRows
}

func (q *FakeQuerier) templatesWithUserNoLock(tpl []database.TemplateTable) []database.Template {
	cpy := make([]database.Template, 0, len(tpl))
	for _, t := range tpl {
		cpy = append(cpy, q.templateWithUserNoLock(t))
	}
	return cpy
}

func (q *FakeQuerier) templateWithUserNoLock(tpl database.TemplateTable) database.Template {
	var user database.User
	for _, _user := range q.users {
		if _user.ID == tpl.CreatedBy {
			user = _user
			break
		}
	}
	var withUser database.Template
	// This is a cheeky way to copy the fields over without explicitly listing them all.
	d, _ := json.Marshal(tpl)
	_ = json.Unmarshal(d, &withUser)
	withUser.CreatedByUsername = user.Username
	withUser.CreatedByAvatarURL = user.AvatarURL
	return withUser
}

func (q *FakeQuerier) templateVersionWithUserNoLock(tpl database.TemplateVersionTable) database.TemplateVersion {
	var user database.User
	for _, _user := range q.users {
		if _user.ID == tpl.CreatedBy {
			user = _user
			break
		}
	}
	var withUser database.TemplateVersion
	// This is a cheeky way to copy the fields over without explicitly listing them all.
	d, _ := json.Marshal(tpl)
	_ = json.Unmarshal(d, &withUser)
	withUser.CreatedByUsername = user.Username
	withUser.CreatedByAvatarURL = user.AvatarURL
	return withUser
}

func (q *FakeQuerier) workspaceBuildWithUserNoLock(tpl database.WorkspaceBuildTable) database.WorkspaceBuild {
	var user database.User
	for _, _user := range q.users {
		if _user.ID == tpl.InitiatorID {
			user = _user
			break
		}
	}
	var withUser database.WorkspaceBuild
	// This is a cheeky way to copy the fields over without explicitly listing them all.
	d, _ := json.Marshal(tpl)
	_ = json.Unmarshal(d, &withUser)
	withUser.InitiatorByUsername = user.Username
	withUser.InitiatorByAvatarUrl = user.AvatarURL
	return withUser
}

func (q *FakeQuerier) getTemplateVersionByIDNoLock(_ context.Context, templateVersionID uuid.UUID) (database.TemplateVersion, error) {
	for _, templateVersion := range q.templateVersions {
		if templateVersion.ID != templateVersionID {
			continue
		}
		return q.templateVersionWithUserNoLock(templateVersion), nil
	}
	return database.TemplateVersion{}, sql.ErrNoRows
}

func (q *FakeQuerier) getWorkspaceAgentByIDNoLock(_ context.Context, id uuid.UUID) (database.WorkspaceAgent, error) {
	// The schema sorts this by created at, so we iterate the array backwards.
	for i := len(q.workspaceAgents) - 1; i >= 0; i-- {
		agent := q.workspaceAgents[i]
		if agent.ID == id {
			return agent, nil
		}
	}
	return database.WorkspaceAgent{}, sql.ErrNoRows
}

func (q *FakeQuerier) getWorkspaceAgentsByResourceIDsNoLock(_ context.Context, resourceIDs []uuid.UUID) ([]database.WorkspaceAgent, error) {
	workspaceAgents := make([]database.WorkspaceAgent, 0)
	for _, agent := range q.workspaceAgents {
		for _, resourceID := range resourceIDs {
			if agent.ResourceID != resourceID {
				continue
			}
			workspaceAgents = append(workspaceAgents, agent)
		}
	}
	return workspaceAgents, nil
}

func (q *FakeQuerier) getProvisionerJobByIDNoLock(_ context.Context, id uuid.UUID) (database.ProvisionerJob, error) {
	for _, provisionerJob := range q.provisionerJobs {
		if provisionerJob.ID != id {
			continue
		}
		return provisionerJob, nil
	}
	return database.ProvisionerJob{}, sql.ErrNoRows
}

func (q *FakeQuerier) getWorkspaceResourcesByJobIDNoLock(_ context.Context, jobID uuid.UUID) ([]database.WorkspaceResource, error) {
	resources := make([]database.WorkspaceResource, 0)
	for _, resource := range q.workspaceResources {
		if resource.JobID != jobID {
			continue
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (q *FakeQuerier) getGroupByIDNoLock(_ context.Context, id uuid.UUID) (database.Group, error) {
	for _, group := range q.groups {
		if group.ID == id {
			return group, nil
		}
	}

	return database.Group{}, sql.ErrNoRows
}

// isNull is only used in dbfake, so reflect is ok. Use this to make the logic
// look more similar to the postgres.
func isNull(v interface{}) bool {
	return !isNotNull(v)
}

func isNotNull(v interface{}) bool {
	return reflect.ValueOf(v).FieldByName("Valid").Bool()
}

// ErrUnimplemented is returned by methods only used by the enterprise/tailnet.pgCoord.  This coordinator explicitly
// depends on  postgres triggers that announce changes on the pubsub.  Implementing support for this in the fake
// database would  strongly couple the FakeQuerier to the pubsub, which is undesirable.  Furthermore, it makes little
// sense to directly  test the pgCoord against anything other than postgres.  The FakeQuerier is designed to allow us to
// test the Coderd  API, and for that kind of test, the in-memory, AGPL tailnet coordinator is sufficient.  Therefore,
// these methods  remain unimplemented in the FakeQuerier.
var ErrUnimplemented = xerrors.New("unimplemented")

func uniqueSortedUUIDs(uuids []uuid.UUID) []uuid.UUID {
	set := make(map[uuid.UUID]struct{})
	for _, id := range uuids {
		set[id] = struct{}{}
	}
	unique := make([]uuid.UUID, 0, len(set))
	for id := range set {
		unique = append(unique, id)
	}
	slices.SortFunc(unique, func(a, b uuid.UUID) bool {
		return a.String() < b.String()
	})
	return unique
}

func (*FakeQuerier) AcquireLock(_ context.Context, _ int64) error {
	return xerrors.New("AcquireLock must only be called within a transaction")
}

func (q *FakeQuerier) AcquireProvisionerJob(_ context.Context, arg database.AcquireProvisionerJobParams) (database.ProvisionerJob, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.ProvisionerJob{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, provisionerJob := range q.provisionerJobs {
		if provisionerJob.StartedAt.Valid {
			continue
		}
		found := false
		for _, provisionerType := range arg.Types {
			if provisionerJob.Provisioner != provisionerType {
				continue
			}
			found = true
			break
		}
		if !found {
			continue
		}
		tags := map[string]string{}
		if arg.Tags != nil {
			err := json.Unmarshal(arg.Tags, &tags)
			if err != nil {
				return provisionerJob, xerrors.Errorf("unmarshal: %w", err)
			}
		}

		missing := false
		for key, value := range provisionerJob.Tags {
			provided, found := tags[key]
			if !found {
				missing = true
				break
			}
			if provided != value {
				missing = true
				break
			}
		}
		if missing {
			continue
		}
		provisionerJob.StartedAt = arg.StartedAt
		provisionerJob.UpdatedAt = arg.StartedAt.Time
		provisionerJob.WorkerID = arg.WorkerID
		q.provisionerJobs[index] = provisionerJob
		return provisionerJob, nil
	}
	return database.ProvisionerJob{}, sql.ErrNoRows
}

func (*FakeQuerier) CleanTailnetCoordinators(_ context.Context) error {
	return ErrUnimplemented
}

func (q *FakeQuerier) DeleteAPIKeyByID(_ context.Context, id string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, apiKey := range q.apiKeys {
		if apiKey.ID != id {
			continue
		}
		q.apiKeys[index] = q.apiKeys[len(q.apiKeys)-1]
		q.apiKeys = q.apiKeys[:len(q.apiKeys)-1]
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) DeleteAPIKeysByUserID(_ context.Context, userID uuid.UUID) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i := len(q.apiKeys) - 1; i >= 0; i-- {
		if q.apiKeys[i].UserID == userID {
			q.apiKeys = append(q.apiKeys[:i], q.apiKeys[i+1:]...)
		}
	}

	return nil
}

func (q *FakeQuerier) DeleteApplicationConnectAPIKeysByUserID(_ context.Context, userID uuid.UUID) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i := len(q.apiKeys) - 1; i >= 0; i-- {
		if q.apiKeys[i].UserID == userID && q.apiKeys[i].Scope == database.APIKeyScopeApplicationConnect {
			q.apiKeys = append(q.apiKeys[:i], q.apiKeys[i+1:]...)
		}
	}

	return nil
}

func (*FakeQuerier) DeleteCoordinator(context.Context, uuid.UUID) error {
	return ErrUnimplemented
}

func (q *FakeQuerier) DeleteGitSSHKey(_ context.Context, userID uuid.UUID) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, key := range q.gitSSHKey {
		if key.UserID != userID {
			continue
		}
		q.gitSSHKey[index] = q.gitSSHKey[len(q.gitSSHKey)-1]
		q.gitSSHKey = q.gitSSHKey[:len(q.gitSSHKey)-1]
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) DeleteGroupByID(_ context.Context, id uuid.UUID) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, group := range q.groups {
		if group.ID == id {
			q.groups = append(q.groups[:i], q.groups[i+1:]...)
			return nil
		}
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) DeleteGroupMemberFromGroup(_ context.Context, arg database.DeleteGroupMemberFromGroupParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, member := range q.groupMembers {
		if member.UserID == arg.UserID && member.GroupID == arg.GroupID {
			q.groupMembers = append(q.groupMembers[:i], q.groupMembers[i+1:]...)
		}
	}
	return nil
}

func (q *FakeQuerier) DeleteGroupMembersByOrgAndUser(_ context.Context, arg database.DeleteGroupMembersByOrgAndUserParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	newMembers := q.groupMembers[:0]
	for _, member := range q.groupMembers {
		if member.UserID != arg.UserID {
			// Do not delete the other members
			newMembers = append(newMembers, member)
		} else if member.UserID == arg.UserID {
			// We only want to delete from groups in the organization in the args.
			for _, group := range q.groups {
				// Find the group that the member is apartof.
				if group.ID == member.GroupID {
					// Only add back the member if the organization ID does not match
					// the arg organization ID. Since the arg is saying which
					// org to delete.
					if group.OrganizationID != arg.OrganizationID {
						newMembers = append(newMembers, member)
					}
					break
				}
			}
		}
	}
	q.groupMembers = newMembers

	return nil
}

func (q *FakeQuerier) DeleteLicense(_ context.Context, id int32) (int32, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, l := range q.licenses {
		if l.ID == id {
			q.licenses[index] = q.licenses[len(q.licenses)-1]
			q.licenses = q.licenses[:len(q.licenses)-1]
			return id, nil
		}
	}
	return 0, sql.ErrNoRows
}

func (*FakeQuerier) DeleteOldWorkspaceAgentLogs(_ context.Context) error {
	// noop
	return nil
}

func (*FakeQuerier) DeleteOldWorkspaceAgentStats(_ context.Context) error {
	// no-op
	return nil
}

func (q *FakeQuerier) DeleteReplicasUpdatedBefore(_ context.Context, before time.Time) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, replica := range q.replicas {
		if replica.UpdatedAt.Before(before) {
			q.replicas = append(q.replicas[:i], q.replicas[i+1:]...)
		}
	}

	return nil
}

func (*FakeQuerier) DeleteTailnetAgent(context.Context, database.DeleteTailnetAgentParams) (database.DeleteTailnetAgentRow, error) {
	return database.DeleteTailnetAgentRow{}, ErrUnimplemented
}

func (*FakeQuerier) DeleteTailnetClient(context.Context, database.DeleteTailnetClientParams) (database.DeleteTailnetClientRow, error) {
	return database.DeleteTailnetClientRow{}, ErrUnimplemented
}

func (q *FakeQuerier) GetAPIKeyByID(_ context.Context, id string) (database.APIKey, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, apiKey := range q.apiKeys {
		if apiKey.ID == id {
			return apiKey, nil
		}
	}
	return database.APIKey{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetAPIKeyByName(_ context.Context, params database.GetAPIKeyByNameParams) (database.APIKey, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if params.TokenName == "" {
		return database.APIKey{}, sql.ErrNoRows
	}
	for _, apiKey := range q.apiKeys {
		if params.UserID == apiKey.UserID && params.TokenName == apiKey.TokenName {
			return apiKey, nil
		}
	}
	return database.APIKey{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetAPIKeysByLoginType(_ context.Context, t database.LoginType) ([]database.APIKey, error) {
	if err := validateDatabaseType(t); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	apiKeys := make([]database.APIKey, 0)
	for _, key := range q.apiKeys {
		if key.LoginType == t {
			apiKeys = append(apiKeys, key)
		}
	}
	return apiKeys, nil
}

func (q *FakeQuerier) GetAPIKeysByUserID(_ context.Context, params database.GetAPIKeysByUserIDParams) ([]database.APIKey, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	apiKeys := make([]database.APIKey, 0)
	for _, key := range q.apiKeys {
		if key.UserID == params.UserID && key.LoginType == params.LoginType {
			apiKeys = append(apiKeys, key)
		}
	}
	return apiKeys, nil
}

func (q *FakeQuerier) GetAPIKeysLastUsedAfter(_ context.Context, after time.Time) ([]database.APIKey, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	apiKeys := make([]database.APIKey, 0)
	for _, key := range q.apiKeys {
		if key.LastUsed.After(after) {
			apiKeys = append(apiKeys, key)
		}
	}
	return apiKeys, nil
}

func (q *FakeQuerier) GetActiveUserCount(_ context.Context) (int64, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	active := int64(0)
	for _, u := range q.users {
		if u.Status == database.UserStatusActive && !u.Deleted {
			active++
		}
	}
	return active, nil
}

func (*FakeQuerier) GetAllTailnetAgents(_ context.Context) ([]database.TailnetAgent, error) {
	return nil, ErrUnimplemented
}

func (*FakeQuerier) GetAllTailnetClients(_ context.Context) ([]database.TailnetClient, error) {
	return nil, ErrUnimplemented
}

func (q *FakeQuerier) GetAppSecurityKey(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.appSecurityKey, nil
}

func (q *FakeQuerier) GetAuditLogsOffset(_ context.Context, arg database.GetAuditLogsOffsetParams) ([]database.GetAuditLogsOffsetRow, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	logs := make([]database.GetAuditLogsOffsetRow, 0, arg.Limit)

	// q.auditLogs are already sorted by time DESC, so no need to sort after the fact.
	for _, alog := range q.auditLogs {
		if arg.Offset > 0 {
			arg.Offset--
			continue
		}
		if arg.Action != "" && !strings.Contains(string(alog.Action), arg.Action) {
			continue
		}
		if arg.ResourceType != "" && !strings.Contains(string(alog.ResourceType), arg.ResourceType) {
			continue
		}
		if arg.ResourceID != uuid.Nil && alog.ResourceID != arg.ResourceID {
			continue
		}
		if arg.Username != "" {
			user, err := q.getUserByIDNoLock(alog.UserID)
			if err == nil && !strings.EqualFold(arg.Username, user.Username) {
				continue
			}
		}
		if arg.Email != "" {
			user, err := q.getUserByIDNoLock(alog.UserID)
			if err == nil && !strings.EqualFold(arg.Email, user.Email) {
				continue
			}
		}
		if !arg.DateFrom.IsZero() {
			if alog.Time.Before(arg.DateFrom) {
				continue
			}
		}
		if !arg.DateTo.IsZero() {
			if alog.Time.After(arg.DateTo) {
				continue
			}
		}
		if arg.BuildReason != "" {
			workspaceBuild, err := q.getWorkspaceBuildByIDNoLock(context.Background(), alog.ResourceID)
			if err == nil && !strings.EqualFold(arg.BuildReason, string(workspaceBuild.Reason)) {
				continue
			}
		}

		user, err := q.getUserByIDNoLock(alog.UserID)
		userValid := err == nil

		logs = append(logs, database.GetAuditLogsOffsetRow{
			ID:               alog.ID,
			RequestID:        alog.RequestID,
			OrganizationID:   alog.OrganizationID,
			Ip:               alog.Ip,
			UserAgent:        alog.UserAgent,
			ResourceType:     alog.ResourceType,
			ResourceID:       alog.ResourceID,
			ResourceTarget:   alog.ResourceTarget,
			ResourceIcon:     alog.ResourceIcon,
			Action:           alog.Action,
			Diff:             alog.Diff,
			StatusCode:       alog.StatusCode,
			AdditionalFields: alog.AdditionalFields,
			UserID:           alog.UserID,
			UserUsername:     sql.NullString{String: user.Username, Valid: userValid},
			UserEmail:        sql.NullString{String: user.Email, Valid: userValid},
			UserCreatedAt:    sql.NullTime{Time: user.CreatedAt, Valid: userValid},
			UserStatus:       database.NullUserStatus{UserStatus: user.Status, Valid: userValid},
			UserRoles:        user.RBACRoles,
			Count:            0,
		})

		if len(logs) >= int(arg.Limit) {
			break
		}
	}

	count := int64(len(logs))
	for i := range logs {
		logs[i].Count = count
	}

	return logs, nil
}

func (q *FakeQuerier) GetAuthorizationUserRoles(_ context.Context, userID uuid.UUID) (database.GetAuthorizationUserRolesRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var user *database.User
	roles := make([]string, 0)
	for _, u := range q.users {
		if u.ID == userID {
			u := u
			roles = append(roles, u.RBACRoles...)
			roles = append(roles, "member")
			user = &u
			break
		}
	}

	for _, mem := range q.organizationMembers {
		if mem.UserID == userID {
			roles = append(roles, mem.Roles...)
			roles = append(roles, "organization-member:"+mem.OrganizationID.String())
		}
	}

	var groups []string
	for _, member := range q.groupMembers {
		if member.UserID == userID {
			groups = append(groups, member.GroupID.String())
		}
	}

	if user == nil {
		return database.GetAuthorizationUserRolesRow{}, sql.ErrNoRows
	}

	return database.GetAuthorizationUserRolesRow{
		ID:       userID,
		Username: user.Username,
		Status:   user.Status,
		Roles:    roles,
		Groups:   groups,
	}, nil
}

func (q *FakeQuerier) GetDERPMeshKey(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.derpMeshKey, nil
}

func (q *FakeQuerier) GetDefaultProxyConfig(_ context.Context) (database.GetDefaultProxyConfigRow, error) {
	return database.GetDefaultProxyConfigRow{
		DisplayName: q.defaultProxyDisplayName,
		IconUrl:     q.defaultProxyIconURL,
	}, nil
}

func (q *FakeQuerier) GetDeploymentDAUs(_ context.Context, tzOffset int32) ([]database.GetDeploymentDAUsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	seens := make(map[time.Time]map[uuid.UUID]struct{})

	for _, as := range q.workspaceAgentStats {
		if as.ConnectionCount == 0 {
			continue
		}
		date := as.CreatedAt.UTC().Add(time.Duration(tzOffset) * -1 * time.Hour).Truncate(time.Hour * 24)

		dateEntry := seens[date]
		if dateEntry == nil {
			dateEntry = make(map[uuid.UUID]struct{})
		}
		dateEntry[as.UserID] = struct{}{}
		seens[date] = dateEntry
	}

	seenKeys := maps.Keys(seens)
	sort.Slice(seenKeys, func(i, j int) bool {
		return seenKeys[i].Before(seenKeys[j])
	})

	var rs []database.GetDeploymentDAUsRow
	for _, key := range seenKeys {
		ids := seens[key]
		for id := range ids {
			rs = append(rs, database.GetDeploymentDAUsRow{
				Date:   key,
				UserID: id,
			})
		}
	}

	return rs, nil
}

func (q *FakeQuerier) GetDeploymentID(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.deploymentID, nil
}

func (q *FakeQuerier) GetDeploymentWorkspaceAgentStats(_ context.Context, createdAfter time.Time) (database.GetDeploymentWorkspaceAgentStatsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	agentStatsCreatedAfter := make([]database.WorkspaceAgentStat, 0)
	for _, agentStat := range q.workspaceAgentStats {
		if agentStat.CreatedAt.After(createdAfter) {
			agentStatsCreatedAfter = append(agentStatsCreatedAfter, agentStat)
		}
	}

	latestAgentStats := map[uuid.UUID]database.WorkspaceAgentStat{}
	for _, agentStat := range q.workspaceAgentStats {
		if agentStat.CreatedAt.After(createdAfter) {
			latestAgentStats[agentStat.AgentID] = agentStat
		}
	}

	stat := database.GetDeploymentWorkspaceAgentStatsRow{}
	for _, agentStat := range latestAgentStats {
		stat.SessionCountVSCode += agentStat.SessionCountVSCode
		stat.SessionCountJetBrains += agentStat.SessionCountJetBrains
		stat.SessionCountReconnectingPTY += agentStat.SessionCountReconnectingPTY
		stat.SessionCountSSH += agentStat.SessionCountSSH
	}

	latencies := make([]float64, 0)
	for _, agentStat := range agentStatsCreatedAfter {
		if agentStat.ConnectionMedianLatencyMS <= 0 {
			continue
		}
		stat.WorkspaceRxBytes += agentStat.RxBytes
		stat.WorkspaceTxBytes += agentStat.TxBytes
		latencies = append(latencies, agentStat.ConnectionMedianLatencyMS)
	}

	tryPercentile := func(fs []float64, p float64) float64 {
		if len(fs) == 0 {
			return -1
		}
		sort.Float64s(fs)
		return fs[int(float64(len(fs))*p/100)]
	}

	stat.WorkspaceConnectionLatency50 = tryPercentile(latencies, 50)
	stat.WorkspaceConnectionLatency95 = tryPercentile(latencies, 95)

	return stat, nil
}

func (q *FakeQuerier) GetDeploymentWorkspaceStats(ctx context.Context) (database.GetDeploymentWorkspaceStatsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	stat := database.GetDeploymentWorkspaceStatsRow{}
	for _, workspace := range q.workspaces {
		build, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, workspace.ID)
		if err != nil {
			return stat, err
		}
		job, err := q.getProvisionerJobByIDNoLock(ctx, build.JobID)
		if err != nil {
			return stat, err
		}
		if !job.StartedAt.Valid {
			stat.PendingWorkspaces++
			continue
		}
		if job.StartedAt.Valid &&
			!job.CanceledAt.Valid &&
			time.Since(job.UpdatedAt) <= 30*time.Second &&
			!job.CompletedAt.Valid {
			stat.BuildingWorkspaces++
			continue
		}
		if job.CompletedAt.Valid &&
			!job.CanceledAt.Valid &&
			!job.Error.Valid {
			if build.Transition == database.WorkspaceTransitionStart {
				stat.RunningWorkspaces++
			}
			if build.Transition == database.WorkspaceTransitionStop {
				stat.StoppedWorkspaces++
			}
			continue
		}
		if job.CanceledAt.Valid || job.Error.Valid {
			stat.FailedWorkspaces++
			continue
		}
	}
	return stat, nil
}

func (q *FakeQuerier) GetFileByHashAndCreator(_ context.Context, arg database.GetFileByHashAndCreatorParams) (database.File, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.File{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, file := range q.files {
		if file.Hash == arg.Hash && file.CreatedBy == arg.CreatedBy {
			return file, nil
		}
	}
	return database.File{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetFileByID(_ context.Context, id uuid.UUID) (database.File, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, file := range q.files {
		if file.ID == id {
			return file, nil
		}
	}
	return database.File{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetFileTemplates(_ context.Context, id uuid.UUID) ([]database.GetFileTemplatesRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	rows := make([]database.GetFileTemplatesRow, 0)
	var file database.File
	for _, f := range q.files {
		if f.ID == id {
			file = f
			break
		}
	}
	if file.Hash == "" {
		return rows, nil
	}

	for _, job := range q.provisionerJobs {
		if job.FileID == id {
			for _, version := range q.templateVersions {
				if version.JobID == job.ID {
					for _, template := range q.templates {
						if template.ID == version.TemplateID.UUID {
							rows = append(rows, database.GetFileTemplatesRow{
								FileID:                 file.ID,
								FileCreatedBy:          file.CreatedBy,
								TemplateID:             template.ID,
								TemplateOrganizationID: template.OrganizationID,
								TemplateCreatedBy:      template.CreatedBy,
								UserACL:                template.UserACL,
								GroupACL:               template.GroupACL,
							})
						}
					}
				}
			}
		}
	}

	return rows, nil
}

func (q *FakeQuerier) GetGitAuthLink(_ context.Context, arg database.GetGitAuthLinkParams) (database.GitAuthLink, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.GitAuthLink{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()
	for _, gitAuthLink := range q.gitAuthLinks {
		if arg.UserID != gitAuthLink.UserID {
			continue
		}
		if arg.ProviderID != gitAuthLink.ProviderID {
			continue
		}
		return gitAuthLink, nil
	}
	return database.GitAuthLink{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetGitSSHKey(_ context.Context, userID uuid.UUID) (database.GitSSHKey, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, key := range q.gitSSHKey {
		if key.UserID == userID {
			return key, nil
		}
	}
	return database.GitSSHKey{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetGroupByID(ctx context.Context, id uuid.UUID) (database.Group, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getGroupByIDNoLock(ctx, id)
}

func (q *FakeQuerier) GetGroupByOrgAndName(_ context.Context, arg database.GetGroupByOrgAndNameParams) (database.Group, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Group{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, group := range q.groups {
		if group.OrganizationID == arg.OrganizationID &&
			group.Name == arg.Name {
			return group, nil
		}
	}

	return database.Group{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetGroupMembers(_ context.Context, groupID uuid.UUID) ([]database.User, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var members []database.GroupMember
	for _, member := range q.groupMembers {
		if member.GroupID == groupID {
			members = append(members, member)
		}
	}

	users := make([]database.User, 0, len(members))

	for _, member := range members {
		for _, user := range q.users {
			if user.ID == member.UserID && user.Status == database.UserStatusActive && !user.Deleted {
				users = append(users, user)
				break
			}
		}
	}

	return users, nil
}

func (q *FakeQuerier) GetGroupsByOrganizationID(_ context.Context, organizationID uuid.UUID) ([]database.Group, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var groups []database.Group
	for _, group := range q.groups {
		// Omit the allUsers group.
		if group.OrganizationID == organizationID && group.ID != organizationID {
			groups = append(groups, group)
		}
	}

	return groups, nil
}

func (q *FakeQuerier) GetHungProvisionerJobs(_ context.Context, hungSince time.Time) ([]database.ProvisionerJob, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	hungJobs := []database.ProvisionerJob{}
	for _, provisionerJob := range q.provisionerJobs {
		if provisionerJob.StartedAt.Valid && !provisionerJob.CompletedAt.Valid && provisionerJob.UpdatedAt.Before(hungSince) {
			hungJobs = append(hungJobs, provisionerJob)
		}
	}
	return hungJobs, nil
}

func (q *FakeQuerier) GetLastUpdateCheck(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if q.lastUpdateCheck == nil {
		return "", sql.ErrNoRows
	}
	return string(q.lastUpdateCheck), nil
}

func (q *FakeQuerier) GetLatestWorkspaceBuildByWorkspaceID(ctx context.Context, workspaceID uuid.UUID) (database.WorkspaceBuild, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, workspaceID)
}

func (q *FakeQuerier) GetLatestWorkspaceBuilds(_ context.Context) ([]database.WorkspaceBuild, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	builds := make(map[uuid.UUID]database.WorkspaceBuild)
	buildNumbers := make(map[uuid.UUID]int32)
	for _, workspaceBuild := range q.workspaceBuilds {
		id := workspaceBuild.WorkspaceID
		if workspaceBuild.BuildNumber > buildNumbers[id] {
			builds[id] = q.workspaceBuildWithUserNoLock(workspaceBuild)
			buildNumbers[id] = workspaceBuild.BuildNumber
		}
	}
	var returnBuilds []database.WorkspaceBuild
	for i, n := range buildNumbers {
		if n > 0 {
			b := builds[i]
			returnBuilds = append(returnBuilds, b)
		}
	}
	if len(returnBuilds) == 0 {
		return nil, sql.ErrNoRows
	}
	return returnBuilds, nil
}

func (q *FakeQuerier) GetLatestWorkspaceBuildsByWorkspaceIDs(_ context.Context, ids []uuid.UUID) ([]database.WorkspaceBuild, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	builds := make(map[uuid.UUID]database.WorkspaceBuild)
	buildNumbers := make(map[uuid.UUID]int32)
	for _, workspaceBuild := range q.workspaceBuilds {
		for _, id := range ids {
			if id == workspaceBuild.WorkspaceID && workspaceBuild.BuildNumber > buildNumbers[id] {
				builds[id] = q.workspaceBuildWithUserNoLock(workspaceBuild)
				buildNumbers[id] = workspaceBuild.BuildNumber
			}
		}
	}
	var returnBuilds []database.WorkspaceBuild
	for i, n := range buildNumbers {
		if n > 0 {
			b := builds[i]
			returnBuilds = append(returnBuilds, b)
		}
	}
	if len(returnBuilds) == 0 {
		return nil, sql.ErrNoRows
	}
	return returnBuilds, nil
}

func (q *FakeQuerier) GetLicenseByID(_ context.Context, id int32) (database.License, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, license := range q.licenses {
		if license.ID == id {
			return license, nil
		}
	}
	return database.License{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetLicenses(_ context.Context) ([]database.License, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	results := append([]database.License{}, q.licenses...)
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

func (q *FakeQuerier) GetLogoURL(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if q.logoURL == "" {
		return "", sql.ErrNoRows
	}

	return q.logoURL, nil
}

func (q *FakeQuerier) GetOAuthSigningKey(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.oauthSigningKey, nil
}

func (q *FakeQuerier) GetOrganizationByID(_ context.Context, id uuid.UUID) (database.Organization, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, organization := range q.organizations {
		if organization.ID == id {
			return organization, nil
		}
	}
	return database.Organization{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetOrganizationByName(_ context.Context, name string) (database.Organization, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, organization := range q.organizations {
		if organization.Name == name {
			return organization, nil
		}
	}
	return database.Organization{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetOrganizationIDsByMemberIDs(_ context.Context, ids []uuid.UUID) ([]database.GetOrganizationIDsByMemberIDsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	getOrganizationIDsByMemberIDRows := make([]database.GetOrganizationIDsByMemberIDsRow, 0, len(ids))
	for _, userID := range ids {
		userOrganizationIDs := make([]uuid.UUID, 0)
		for _, membership := range q.organizationMembers {
			if membership.UserID == userID {
				userOrganizationIDs = append(userOrganizationIDs, membership.OrganizationID)
			}
		}
		getOrganizationIDsByMemberIDRows = append(getOrganizationIDsByMemberIDRows, database.GetOrganizationIDsByMemberIDsRow{
			UserID:          userID,
			OrganizationIDs: userOrganizationIDs,
		})
	}
	if len(getOrganizationIDsByMemberIDRows) == 0 {
		return nil, sql.ErrNoRows
	}
	return getOrganizationIDsByMemberIDRows, nil
}

func (q *FakeQuerier) GetOrganizationMemberByUserID(_ context.Context, arg database.GetOrganizationMemberByUserIDParams) (database.OrganizationMember, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.OrganizationMember{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, organizationMember := range q.organizationMembers {
		if organizationMember.OrganizationID != arg.OrganizationID {
			continue
		}
		if organizationMember.UserID != arg.UserID {
			continue
		}
		return organizationMember, nil
	}
	return database.OrganizationMember{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetOrganizationMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]database.OrganizationMember, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var memberships []database.OrganizationMember
	for _, organizationMember := range q.organizationMembers {
		mem := organizationMember
		if mem.UserID != userID {
			continue
		}
		memberships = append(memberships, mem)
	}
	return memberships, nil
}

func (q *FakeQuerier) GetOrganizations(_ context.Context) ([]database.Organization, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if len(q.organizations) == 0 {
		return nil, sql.ErrNoRows
	}
	return q.organizations, nil
}

func (q *FakeQuerier) GetOrganizationsByUserID(_ context.Context, userID uuid.UUID) ([]database.Organization, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	organizations := make([]database.Organization, 0)
	for _, organizationMember := range q.organizationMembers {
		if organizationMember.UserID != userID {
			continue
		}
		for _, organization := range q.organizations {
			if organization.ID != organizationMember.OrganizationID {
				continue
			}
			organizations = append(organizations, organization)
		}
	}
	if len(organizations) == 0 {
		return nil, sql.ErrNoRows
	}
	return organizations, nil
}

func (q *FakeQuerier) GetParameterSchemasByJobID(_ context.Context, jobID uuid.UUID) ([]database.ParameterSchema, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	parameters := make([]database.ParameterSchema, 0)
	for _, parameterSchema := range q.parameterSchemas {
		if parameterSchema.JobID != jobID {
			continue
		}
		parameters = append(parameters, parameterSchema)
	}
	if len(parameters) == 0 {
		return nil, sql.ErrNoRows
	}
	sort.Slice(parameters, func(i, j int) bool {
		return parameters[i].Index < parameters[j].Index
	})
	return parameters, nil
}

func (q *FakeQuerier) GetPreviousTemplateVersion(_ context.Context, arg database.GetPreviousTemplateVersionParams) (database.TemplateVersion, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.TemplateVersion{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var currentTemplateVersion database.TemplateVersion
	for _, templateVersion := range q.templateVersions {
		if templateVersion.TemplateID != arg.TemplateID {
			continue
		}
		if templateVersion.Name != arg.Name {
			continue
		}
		if templateVersion.OrganizationID != arg.OrganizationID {
			continue
		}
		currentTemplateVersion = q.templateVersionWithUserNoLock(templateVersion)
		break
	}

	previousTemplateVersions := make([]database.TemplateVersion, 0)
	for _, templateVersion := range q.templateVersions {
		if templateVersion.ID == currentTemplateVersion.ID {
			continue
		}
		if templateVersion.OrganizationID != arg.OrganizationID {
			continue
		}
		if templateVersion.TemplateID != currentTemplateVersion.TemplateID {
			continue
		}

		if templateVersion.CreatedAt.Before(currentTemplateVersion.CreatedAt) {
			previousTemplateVersions = append(previousTemplateVersions, q.templateVersionWithUserNoLock(templateVersion))
		}
	}

	if len(previousTemplateVersions) == 0 {
		return database.TemplateVersion{}, sql.ErrNoRows
	}

	sort.Slice(previousTemplateVersions, func(i, j int) bool {
		return previousTemplateVersions[i].CreatedAt.After(previousTemplateVersions[j].CreatedAt)
	})

	return previousTemplateVersions[0], nil
}

func (q *FakeQuerier) GetProvisionerDaemons(_ context.Context) ([]database.ProvisionerDaemon, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if len(q.provisionerDaemons) == 0 {
		return nil, sql.ErrNoRows
	}
	return q.provisionerDaemons, nil
}

func (q *FakeQuerier) GetProvisionerJobByID(ctx context.Context, id uuid.UUID) (database.ProvisionerJob, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getProvisionerJobByIDNoLock(ctx, id)
}

func (q *FakeQuerier) GetProvisionerJobsByIDs(_ context.Context, ids []uuid.UUID) ([]database.ProvisionerJob, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	jobs := make([]database.ProvisionerJob, 0)
	for _, job := range q.provisionerJobs {
		for _, id := range ids {
			if id == job.ID {
				jobs = append(jobs, job)
				break
			}
		}
	}
	if len(jobs) == 0 {
		return nil, sql.ErrNoRows
	}

	return jobs, nil
}

func (q *FakeQuerier) GetProvisionerJobsByIDsWithQueuePosition(_ context.Context, ids []uuid.UUID) ([]database.GetProvisionerJobsByIDsWithQueuePositionRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	jobs := make([]database.GetProvisionerJobsByIDsWithQueuePositionRow, 0)
	queuePosition := int64(1)
	for _, job := range q.provisionerJobs {
		for _, id := range ids {
			if id == job.ID {
				job := database.GetProvisionerJobsByIDsWithQueuePositionRow{
					ProvisionerJob: job,
				}
				if !job.ProvisionerJob.StartedAt.Valid {
					job.QueuePosition = queuePosition
				}
				jobs = append(jobs, job)
				break
			}
		}
		if !job.StartedAt.Valid {
			queuePosition++
		}
	}
	for _, job := range jobs {
		if !job.ProvisionerJob.StartedAt.Valid {
			// Set it to the max position!
			job.QueueSize = queuePosition
		}
	}
	return jobs, nil
}

func (q *FakeQuerier) GetProvisionerJobsCreatedAfter(_ context.Context, after time.Time) ([]database.ProvisionerJob, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	jobs := make([]database.ProvisionerJob, 0)
	for _, job := range q.provisionerJobs {
		if job.CreatedAt.After(after) {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (q *FakeQuerier) GetProvisionerLogsAfterID(_ context.Context, arg database.GetProvisionerLogsAfterIDParams) ([]database.ProvisionerJobLog, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	logs := make([]database.ProvisionerJobLog, 0)
	for _, jobLog := range q.provisionerJobLogs {
		if jobLog.JobID != arg.JobID {
			continue
		}
		if jobLog.ID <= arg.CreatedAfter {
			continue
		}
		logs = append(logs, jobLog)
	}
	return logs, nil
}

func (q *FakeQuerier) GetQuotaAllowanceForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var sum int64
	for _, member := range q.groupMembers {
		if member.UserID != userID {
			continue
		}
		for _, group := range q.groups {
			if group.ID == member.GroupID {
				sum += int64(group.QuotaAllowance)
			}
		}
	}
	return sum, nil
}

func (q *FakeQuerier) GetQuotaConsumedForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var sum int64
	for _, workspace := range q.workspaces {
		if workspace.OwnerID != userID {
			continue
		}
		if workspace.Deleted {
			continue
		}

		var lastBuild database.WorkspaceBuildTable
		for _, build := range q.workspaceBuilds {
			if build.WorkspaceID != workspace.ID {
				continue
			}
			if build.CreatedAt.After(lastBuild.CreatedAt) {
				lastBuild = build
			}
		}
		sum += int64(lastBuild.DailyCost)
	}
	return sum, nil
}

func (q *FakeQuerier) GetReplicaByID(_ context.Context, id uuid.UUID) (database.Replica, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, replica := range q.replicas {
		if replica.ID == id {
			return replica, nil
		}
	}

	return database.Replica{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetReplicasUpdatedAfter(_ context.Context, updatedAt time.Time) ([]database.Replica, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	replicas := make([]database.Replica, 0)
	for _, replica := range q.replicas {
		if replica.UpdatedAt.After(updatedAt) && !replica.StoppedAt.Valid {
			replicas = append(replicas, replica)
		}
	}
	return replicas, nil
}

func (q *FakeQuerier) GetServiceBanner(_ context.Context) (string, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if q.serviceBanner == nil {
		return "", sql.ErrNoRows
	}

	return string(q.serviceBanner), nil
}

func (*FakeQuerier) GetTailnetAgents(context.Context, uuid.UUID) ([]database.TailnetAgent, error) {
	return nil, ErrUnimplemented
}

func (*FakeQuerier) GetTailnetClientsForAgent(context.Context, uuid.UUID) ([]database.TailnetClient, error) {
	return nil, ErrUnimplemented
}

func (q *FakeQuerier) GetTemplateAverageBuildTime(ctx context.Context, arg database.GetTemplateAverageBuildTimeParams) (database.GetTemplateAverageBuildTimeRow, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.GetTemplateAverageBuildTimeRow{}, err
	}

	var emptyRow database.GetTemplateAverageBuildTimeRow
	var (
		startTimes  []float64
		stopTimes   []float64
		deleteTimes []float64
	)
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	for _, wb := range q.workspaceBuilds {
		version, err := q.getTemplateVersionByIDNoLock(ctx, wb.TemplateVersionID)
		if err != nil {
			return emptyRow, err
		}
		if version.TemplateID != arg.TemplateID {
			continue
		}

		job, err := q.getProvisionerJobByIDNoLock(ctx, wb.JobID)
		if err != nil {
			return emptyRow, err
		}
		if job.CompletedAt.Valid {
			took := job.CompletedAt.Time.Sub(job.StartedAt.Time).Seconds()
			switch wb.Transition {
			case database.WorkspaceTransitionStart:
				startTimes = append(startTimes, took)
			case database.WorkspaceTransitionStop:
				stopTimes = append(stopTimes, took)
			case database.WorkspaceTransitionDelete:
				deleteTimes = append(deleteTimes, took)
			}
		}
	}

	tryPercentile := func(fs []float64, p float64) float64 {
		if len(fs) == 0 {
			return -1
		}
		sort.Float64s(fs)
		return fs[int(float64(len(fs))*p/100)]
	}

	var row database.GetTemplateAverageBuildTimeRow
	row.Delete50, row.Delete95 = tryPercentile(deleteTimes, 50), tryPercentile(deleteTimes, 95)
	row.Stop50, row.Stop95 = tryPercentile(stopTimes, 50), tryPercentile(stopTimes, 95)
	row.Start50, row.Start95 = tryPercentile(startTimes, 50), tryPercentile(startTimes, 95)
	return row, nil
}

func (q *FakeQuerier) GetTemplateByID(ctx context.Context, id uuid.UUID) (database.Template, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getTemplateByIDNoLock(ctx, id)
}

func (q *FakeQuerier) GetTemplateByOrganizationAndName(_ context.Context, arg database.GetTemplateByOrganizationAndNameParams) (database.Template, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Template{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, template := range q.templates {
		if template.OrganizationID != arg.OrganizationID {
			continue
		}
		if !strings.EqualFold(template.Name, arg.Name) {
			continue
		}
		if template.Deleted != arg.Deleted {
			continue
		}
		return q.templateWithUserNoLock(template), nil
	}
	return database.Template{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetTemplateDAUs(_ context.Context, arg database.GetTemplateDAUsParams) ([]database.GetTemplateDAUsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	seens := make(map[time.Time]map[uuid.UUID]struct{})

	for _, as := range q.workspaceAgentStats {
		if as.TemplateID != arg.TemplateID {
			continue
		}
		if as.ConnectionCount == 0 {
			continue
		}

		date := as.CreatedAt.UTC().Add(time.Duration(arg.TzOffset) * time.Hour * -1).Truncate(time.Hour * 24)

		dateEntry := seens[date]
		if dateEntry == nil {
			dateEntry = make(map[uuid.UUID]struct{})
		}
		dateEntry[as.UserID] = struct{}{}
		seens[date] = dateEntry
	}

	seenKeys := maps.Keys(seens)
	sort.Slice(seenKeys, func(i, j int) bool {
		return seenKeys[i].Before(seenKeys[j])
	})

	var rs []database.GetTemplateDAUsRow
	for _, key := range seenKeys {
		ids := seens[key]
		for id := range ids {
			rs = append(rs, database.GetTemplateDAUsRow{
				Date:   key,
				UserID: id,
			})
		}
	}

	return rs, nil
}

func (q *FakeQuerier) GetTemplateDailyInsights(_ context.Context, arg database.GetTemplateDailyInsightsParams) ([]database.GetTemplateDailyInsightsRow, error) {
	err := validateDatabaseType(arg)
	if err != nil {
		return nil, err
	}

	type dailyStat struct {
		startTime, endTime time.Time
		userSet            map[uuid.UUID]struct{}
		templateIDSet      map[uuid.UUID]struct{}
	}
	dailyStats := []dailyStat{{arg.StartTime, arg.StartTime.AddDate(0, 0, 1), make(map[uuid.UUID]struct{}), make(map[uuid.UUID]struct{})}}
	for dailyStats[len(dailyStats)-1].endTime.Before(arg.EndTime) {
		dailyStats = append(dailyStats, dailyStat{dailyStats[len(dailyStats)-1].endTime, dailyStats[len(dailyStats)-1].endTime.AddDate(0, 0, 1), make(map[uuid.UUID]struct{}), make(map[uuid.UUID]struct{})})
	}
	if dailyStats[len(dailyStats)-1].endTime.After(arg.EndTime) {
		dailyStats[len(dailyStats)-1].endTime = arg.EndTime
	}

	for _, s := range q.workspaceAgentStats {
		if s.CreatedAt.Before(arg.StartTime) || s.CreatedAt.Equal(arg.EndTime) || s.CreatedAt.After(arg.EndTime) {
			continue
		}
		if len(arg.TemplateIDs) > 0 && !slices.Contains(arg.TemplateIDs, s.TemplateID) {
			continue
		}
		if s.ConnectionCount == 0 {
			continue
		}

		for _, ds := range dailyStats {
			if s.CreatedAt.Before(ds.startTime) || s.CreatedAt.Equal(ds.endTime) || s.CreatedAt.After(ds.endTime) {
				continue
			}
			ds.userSet[s.UserID] = struct{}{}
			ds.templateIDSet[s.TemplateID] = struct{}{}
			break
		}
	}

	var result []database.GetTemplateDailyInsightsRow
	for _, ds := range dailyStats {
		templateIDs := make([]uuid.UUID, 0, len(ds.templateIDSet))
		for templateID := range ds.templateIDSet {
			templateIDs = append(templateIDs, templateID)
		}
		slices.SortFunc(templateIDs, func(a, b uuid.UUID) bool {
			return a.String() < b.String()
		})
		result = append(result, database.GetTemplateDailyInsightsRow{
			StartTime:   ds.startTime,
			EndTime:     ds.endTime,
			TemplateIDs: templateIDs,
			ActiveUsers: int64(len(ds.userSet)),
		})
	}
	return result, nil
}

func (q *FakeQuerier) GetTemplateInsights(_ context.Context, arg database.GetTemplateInsightsParams) (database.GetTemplateInsightsRow, error) {
	err := validateDatabaseType(arg)
	if err != nil {
		return database.GetTemplateInsightsRow{}, err
	}

	templateIDSet := make(map[uuid.UUID]struct{})
	appUsageIntervalsByUser := make(map[uuid.UUID]map[time.Time]*database.GetTemplateInsightsRow)
	for _, s := range q.workspaceAgentStats {
		if s.CreatedAt.Before(arg.StartTime) || s.CreatedAt.Equal(arg.EndTime) || s.CreatedAt.After(arg.EndTime) {
			continue
		}
		if len(arg.TemplateIDs) > 0 && !slices.Contains(arg.TemplateIDs, s.TemplateID) {
			continue
		}
		if s.ConnectionCount == 0 {
			continue
		}

		templateIDSet[s.TemplateID] = struct{}{}
		if appUsageIntervalsByUser[s.UserID] == nil {
			appUsageIntervalsByUser[s.UserID] = make(map[time.Time]*database.GetTemplateInsightsRow)
		}
		t := s.CreatedAt.Truncate(5 * time.Minute)
		if _, ok := appUsageIntervalsByUser[s.UserID][t]; !ok {
			appUsageIntervalsByUser[s.UserID][t] = &database.GetTemplateInsightsRow{}
		}

		if s.SessionCountJetBrains > 0 {
			appUsageIntervalsByUser[s.UserID][t].UsageJetbrainsSeconds = 300
		}
		if s.SessionCountVSCode > 0 {
			appUsageIntervalsByUser[s.UserID][t].UsageVscodeSeconds = 300
		}
		if s.SessionCountReconnectingPTY > 0 {
			appUsageIntervalsByUser[s.UserID][t].UsageReconnectingPtySeconds = 300
		}
		if s.SessionCountSSH > 0 {
			appUsageIntervalsByUser[s.UserID][t].UsageSshSeconds = 300
		}
	}

	templateIDs := make([]uuid.UUID, 0, len(templateIDSet))
	for templateID := range templateIDSet {
		templateIDs = append(templateIDs, templateID)
	}
	slices.SortFunc(templateIDs, func(a, b uuid.UUID) bool {
		return a.String() < b.String()
	})
	result := database.GetTemplateInsightsRow{
		TemplateIDs: templateIDs,
		ActiveUsers: int64(len(appUsageIntervalsByUser)),
	}
	for _, intervals := range appUsageIntervalsByUser {
		for _, interval := range intervals {
			result.UsageJetbrainsSeconds += interval.UsageJetbrainsSeconds
			result.UsageVscodeSeconds += interval.UsageVscodeSeconds
			result.UsageReconnectingPtySeconds += interval.UsageReconnectingPtySeconds
			result.UsageSshSeconds += interval.UsageSshSeconds
		}
	}
	return result, nil
}

func (q *FakeQuerier) GetTemplateParameterInsights(ctx context.Context, arg database.GetTemplateParameterInsightsParams) ([]database.GetTemplateParameterInsightsRow, error) {
	err := validateDatabaseType(arg)
	if err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// WITH latest_workspace_builds ...
	latestWorkspaceBuilds := make(map[uuid.UUID]database.WorkspaceBuildTable)
	for _, wb := range q.workspaceBuilds {
		if wb.CreatedAt.Before(arg.StartTime) || wb.CreatedAt.Equal(arg.EndTime) || wb.CreatedAt.After(arg.EndTime) {
			continue
		}
		if latestWorkspaceBuilds[wb.WorkspaceID].BuildNumber < wb.BuildNumber {
			latestWorkspaceBuilds[wb.WorkspaceID] = wb
		}
	}
	if len(arg.TemplateIDs) > 0 {
		for wsID := range latestWorkspaceBuilds {
			ws, err := q.getWorkspaceByIDNoLock(ctx, wsID)
			if err != nil {
				return nil, err
			}
			if slices.Contains(arg.TemplateIDs, ws.TemplateID) {
				delete(latestWorkspaceBuilds, wsID)
			}
		}
	}
	// WITH unique_template_params ...
	num := int64(0)
	uniqueTemplateParams := make(map[string]*database.GetTemplateParameterInsightsRow)
	uniqueTemplateParamWorkspaceBuildIDs := make(map[string][]uuid.UUID)
	for _, wb := range latestWorkspaceBuilds {
		tv, err := q.getTemplateVersionByIDNoLock(ctx, wb.TemplateVersionID)
		if err != nil {
			return nil, err
		}
		for _, tvp := range q.templateVersionParameters {
			if tvp.TemplateVersionID != tv.ID {
				continue
			}
			key := fmt.Sprintf("%s:%s:%s:%s", tvp.Name, tvp.DisplayName, tvp.Description, tvp.Options)
			if _, ok := uniqueTemplateParams[key]; !ok {
				num++
				uniqueTemplateParams[key] = &database.GetTemplateParameterInsightsRow{
					Num:         num,
					Name:        tvp.Name,
					DisplayName: tvp.DisplayName,
					Description: tvp.Description,
					Options:     tvp.Options,
				}
			}
			uniqueTemplateParams[key].TemplateIDs = append(uniqueTemplateParams[key].TemplateIDs, tv.TemplateID.UUID)
			uniqueTemplateParamWorkspaceBuildIDs[key] = append(uniqueTemplateParamWorkspaceBuildIDs[key], wb.ID)
		}
	}
	// SELECT ...
	counts := make(map[string]map[string]int64)
	for key, utp := range uniqueTemplateParams {
		for _, wbp := range q.workspaceBuildParameters {
			if !slices.Contains(uniqueTemplateParamWorkspaceBuildIDs[key], wbp.WorkspaceBuildID) {
				continue
			}
			if wbp.Name != utp.Name {
				continue
			}
			if counts[key] == nil {
				counts[key] = make(map[string]int64)
			}
			counts[key][wbp.Value]++
		}
	}

	var rows []database.GetTemplateParameterInsightsRow
	for key, utp := range uniqueTemplateParams {
		for value, count := range counts[key] {
			rows = append(rows, database.GetTemplateParameterInsightsRow{
				Num:         utp.Num,
				TemplateIDs: uniqueSortedUUIDs(utp.TemplateIDs),
				Name:        utp.Name,
				DisplayName: utp.DisplayName,
				Description: utp.Description,
				Options:     utp.Options,
				Value:       value,
				Count:       count,
			})
		}
	}

	return rows, nil
}

func (q *FakeQuerier) GetTemplateVersionByID(ctx context.Context, templateVersionID uuid.UUID) (database.TemplateVersion, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getTemplateVersionByIDNoLock(ctx, templateVersionID)
}

func (q *FakeQuerier) GetTemplateVersionByJobID(_ context.Context, jobID uuid.UUID) (database.TemplateVersion, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, templateVersion := range q.templateVersions {
		if templateVersion.JobID != jobID {
			continue
		}
		return q.templateVersionWithUserNoLock(templateVersion), nil
	}
	return database.TemplateVersion{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetTemplateVersionByTemplateIDAndName(_ context.Context, arg database.GetTemplateVersionByTemplateIDAndNameParams) (database.TemplateVersion, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.TemplateVersion{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, templateVersion := range q.templateVersions {
		if templateVersion.TemplateID != arg.TemplateID {
			continue
		}
		if !strings.EqualFold(templateVersion.Name, arg.Name) {
			continue
		}
		return q.templateVersionWithUserNoLock(templateVersion), nil
	}
	return database.TemplateVersion{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetTemplateVersionParameters(_ context.Context, templateVersionID uuid.UUID) ([]database.TemplateVersionParameter, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	parameters := make([]database.TemplateVersionParameter, 0)
	for _, param := range q.templateVersionParameters {
		if param.TemplateVersionID != templateVersionID {
			continue
		}
		parameters = append(parameters, param)
	}
	sort.Slice(parameters, func(i, j int) bool {
		if parameters[i].DisplayOrder != parameters[j].DisplayOrder {
			return parameters[i].DisplayOrder < parameters[j].DisplayOrder
		}
		return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
	})
	return parameters, nil
}

func (q *FakeQuerier) GetTemplateVersionVariables(_ context.Context, templateVersionID uuid.UUID) ([]database.TemplateVersionVariable, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	variables := make([]database.TemplateVersionVariable, 0)
	for _, variable := range q.templateVersionVariables {
		if variable.TemplateVersionID != templateVersionID {
			continue
		}
		variables = append(variables, variable)
	}
	return variables, nil
}

func (q *FakeQuerier) GetTemplateVersionsByIDs(_ context.Context, ids []uuid.UUID) ([]database.TemplateVersion, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	versions := make([]database.TemplateVersion, 0)
	for _, version := range q.templateVersions {
		for _, id := range ids {
			if id == version.ID {
				versions = append(versions, q.templateVersionWithUserNoLock(version))
				break
			}
		}
	}
	if len(versions) == 0 {
		return nil, sql.ErrNoRows
	}

	return versions, nil
}

func (q *FakeQuerier) GetTemplateVersionsByTemplateID(_ context.Context, arg database.GetTemplateVersionsByTemplateIDParams) (version []database.TemplateVersion, err error) {
	if err := validateDatabaseType(arg); err != nil {
		return version, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, templateVersion := range q.templateVersions {
		if templateVersion.TemplateID.UUID != arg.TemplateID {
			continue
		}
		version = append(version, q.templateVersionWithUserNoLock(templateVersion))
	}

	// Database orders by created_at
	slices.SortFunc(version, func(a, b database.TemplateVersion) bool {
		if a.CreatedAt.Equal(b.CreatedAt) {
			// Technically the postgres database also orders by uuid. So match
			// that behavior
			return a.ID.String() < b.ID.String()
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})

	if arg.AfterID != uuid.Nil {
		found := false
		for i, v := range version {
			if v.ID == arg.AfterID {
				// We want to return all users after index i.
				version = version[i+1:]
				found = true
				break
			}
		}

		// If no users after the time, then we return an empty list.
		if !found {
			return nil, sql.ErrNoRows
		}
	}

	if arg.OffsetOpt > 0 {
		if int(arg.OffsetOpt) > len(version)-1 {
			return nil, sql.ErrNoRows
		}
		version = version[arg.OffsetOpt:]
	}

	if arg.LimitOpt > 0 {
		if int(arg.LimitOpt) > len(version) {
			arg.LimitOpt = int32(len(version))
		}
		version = version[:arg.LimitOpt]
	}

	if len(version) == 0 {
		return nil, sql.ErrNoRows
	}

	return version, nil
}

func (q *FakeQuerier) GetTemplateVersionsCreatedAfter(_ context.Context, after time.Time) ([]database.TemplateVersion, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	versions := make([]database.TemplateVersion, 0)
	for _, version := range q.templateVersions {
		if version.CreatedAt.After(after) {
			versions = append(versions, q.templateVersionWithUserNoLock(version))
		}
	}
	return versions, nil
}

func (q *FakeQuerier) GetTemplates(_ context.Context) ([]database.Template, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	templates := slices.Clone(q.templates)
	slices.SortFunc(templates, func(i, j database.TemplateTable) bool {
		if i.Name != j.Name {
			return i.Name < j.Name
		}
		return i.ID.String() < j.ID.String()
	})

	return q.templatesWithUserNoLock(templates), nil
}

func (q *FakeQuerier) GetTemplatesWithFilter(ctx context.Context, arg database.GetTemplatesWithFilterParams) ([]database.Template, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	return q.GetAuthorizedTemplates(ctx, arg, nil)
}

func (q *FakeQuerier) GetUnexpiredLicenses(_ context.Context) ([]database.License, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	now := time.Now()
	var results []database.License
	for _, l := range q.licenses {
		if l.Exp.After(now) {
			results = append(results, l)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

func (q *FakeQuerier) GetUserByEmailOrUsername(_ context.Context, arg database.GetUserByEmailOrUsernameParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, user := range q.users {
		if !user.Deleted && (strings.EqualFold(user.Email, arg.Email) || strings.EqualFold(user.Username, arg.Username)) {
			return user, nil
		}
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetUserByID(_ context.Context, id uuid.UUID) (database.User, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getUserByIDNoLock(id)
}

func (q *FakeQuerier) GetUserCount(_ context.Context) (int64, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	existing := int64(0)
	for _, u := range q.users {
		if !u.Deleted {
			existing++
		}
	}
	return existing, nil
}

func (q *FakeQuerier) GetUserLatencyInsights(_ context.Context, arg database.GetUserLatencyInsightsParams) ([]database.GetUserLatencyInsightsRow, error) {
	err := validateDatabaseType(arg)
	if err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	latenciesByUserID := make(map[uuid.UUID][]float64)
	seenTemplatesByUserID := make(map[uuid.UUID]map[uuid.UUID]struct{})
	for _, s := range q.workspaceAgentStats {
		if len(arg.TemplateIDs) > 0 && !slices.Contains(arg.TemplateIDs, s.TemplateID) {
			continue
		}
		if !arg.StartTime.Equal(s.CreatedAt) && (s.CreatedAt.Before(arg.StartTime) || s.CreatedAt.After(arg.EndTime)) {
			continue
		}
		if s.ConnectionCount == 0 {
			continue
		}
		if s.ConnectionMedianLatencyMS <= 0 {
			continue
		}

		latenciesByUserID[s.UserID] = append(latenciesByUserID[s.UserID], s.ConnectionMedianLatencyMS)
		if seenTemplatesByUserID[s.UserID] == nil {
			seenTemplatesByUserID[s.UserID] = make(map[uuid.UUID]struct{})
		}
		seenTemplatesByUserID[s.UserID][s.TemplateID] = struct{}{}
	}

	tryPercentile := func(fs []float64, p float64) float64 {
		if len(fs) == 0 {
			return -1
		}
		sort.Float64s(fs)
		return fs[int(float64(len(fs))*p/100)]
	}

	var rows []database.GetUserLatencyInsightsRow
	for userID, latencies := range latenciesByUserID {
		sort.Float64s(latencies)
		templateIDSet := seenTemplatesByUserID[userID]
		templateIDs := make([]uuid.UUID, 0, len(templateIDSet))
		for templateID := range templateIDSet {
			templateIDs = append(templateIDs, templateID)
		}
		slices.SortFunc(templateIDs, func(a, b uuid.UUID) bool {
			return a.String() < b.String()
		})
		user, err := q.getUserByIDNoLock(userID)
		if err != nil {
			return nil, err
		}
		row := database.GetUserLatencyInsightsRow{
			UserID:                       userID,
			Username:                     user.Username,
			AvatarURL:                    user.AvatarURL,
			TemplateIDs:                  templateIDs,
			WorkspaceConnectionLatency50: tryPercentile(latencies, 50),
			WorkspaceConnectionLatency95: tryPercentile(latencies, 95),
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b database.GetUserLatencyInsightsRow) bool {
		return a.UserID.String() < b.UserID.String()
	})

	return rows, nil
}

func (q *FakeQuerier) GetUserLinkByLinkedID(_ context.Context, id string) (database.UserLink, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, link := range q.userLinks {
		if link.LinkedID == id {
			return link, nil
		}
	}
	return database.UserLink{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetUserLinkByUserIDLoginType(_ context.Context, params database.GetUserLinkByUserIDLoginTypeParams) (database.UserLink, error) {
	if err := validateDatabaseType(params); err != nil {
		return database.UserLink{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, link := range q.userLinks {
		if link.UserID == params.UserID && link.LoginType == params.LoginType {
			return link, nil
		}
	}
	return database.UserLink{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetUsers(_ context.Context, params database.GetUsersParams) ([]database.GetUsersRow, error) {
	if err := validateDatabaseType(params); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// Avoid side-effect of sorting.
	users := make([]database.User, len(q.users))
	copy(users, q.users)

	// Database orders by username
	slices.SortFunc(users, func(a, b database.User) bool {
		return strings.ToLower(a.Username) < strings.ToLower(b.Username)
	})

	// Filter out deleted since they should never be returned..
	tmp := make([]database.User, 0, len(users))
	for _, user := range users {
		if !user.Deleted {
			tmp = append(tmp, user)
		}
	}
	users = tmp

	if params.AfterID != uuid.Nil {
		found := false
		for i, v := range users {
			if v.ID == params.AfterID {
				// We want to return all users after index i.
				users = users[i+1:]
				found = true
				break
			}
		}

		// If no users after the time, then we return an empty list.
		if !found {
			return []database.GetUsersRow{}, nil
		}
	}

	if params.Search != "" {
		tmp := make([]database.User, 0, len(users))
		for i, user := range users {
			if strings.Contains(strings.ToLower(user.Email), strings.ToLower(params.Search)) {
				tmp = append(tmp, users[i])
			} else if strings.Contains(strings.ToLower(user.Username), strings.ToLower(params.Search)) {
				tmp = append(tmp, users[i])
			}
		}
		users = tmp
	}

	if len(params.Status) > 0 {
		usersFilteredByStatus := make([]database.User, 0, len(users))
		for i, user := range users {
			if slice.ContainsCompare(params.Status, user.Status, func(a, b database.UserStatus) bool {
				return strings.EqualFold(string(a), string(b))
			}) {
				usersFilteredByStatus = append(usersFilteredByStatus, users[i])
			}
		}
		users = usersFilteredByStatus
	}

	if len(params.RbacRole) > 0 && !slice.Contains(params.RbacRole, rbac.RoleMember()) {
		usersFilteredByRole := make([]database.User, 0, len(users))
		for i, user := range users {
			if slice.OverlapCompare(params.RbacRole, user.RBACRoles, strings.EqualFold) {
				usersFilteredByRole = append(usersFilteredByRole, users[i])
			}
		}
		users = usersFilteredByRole
	}

	if !params.LastSeenBefore.IsZero() {
		usersFilteredByLastSeen := make([]database.User, 0, len(users))
		for i, user := range users {
			if user.LastSeenAt.Before(params.LastSeenBefore) {
				usersFilteredByLastSeen = append(usersFilteredByLastSeen, users[i])
			}
		}
		users = usersFilteredByLastSeen
	}

	if !params.LastSeenAfter.IsZero() {
		usersFilteredByLastSeen := make([]database.User, 0, len(users))
		for i, user := range users {
			if user.LastSeenAt.After(params.LastSeenAfter) {
				usersFilteredByLastSeen = append(usersFilteredByLastSeen, users[i])
			}
		}
		users = usersFilteredByLastSeen
	}

	beforePageCount := len(users)

	if params.OffsetOpt > 0 {
		if int(params.OffsetOpt) > len(users)-1 {
			return []database.GetUsersRow{}, nil
		}
		users = users[params.OffsetOpt:]
	}

	if params.LimitOpt > 0 {
		if int(params.LimitOpt) > len(users) {
			params.LimitOpt = int32(len(users))
		}
		users = users[:params.LimitOpt]
	}

	return convertUsers(users, int64(beforePageCount)), nil
}

func (q *FakeQuerier) GetUsersByIDs(_ context.Context, ids []uuid.UUID) ([]database.User, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	users := make([]database.User, 0)
	for _, user := range q.users {
		for _, id := range ids {
			if user.ID != id {
				continue
			}
			users = append(users, user)
		}
	}
	return users, nil
}

func (q *FakeQuerier) GetWorkspaceAgentByAuthToken(_ context.Context, authToken uuid.UUID) (database.WorkspaceAgent, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// The schema sorts this by created at, so we iterate the array backwards.
	for i := len(q.workspaceAgents) - 1; i >= 0; i-- {
		agent := q.workspaceAgents[i]
		if agent.AuthToken == authToken {
			return agent, nil
		}
	}
	return database.WorkspaceAgent{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceAgentByID(ctx context.Context, id uuid.UUID) (database.WorkspaceAgent, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getWorkspaceAgentByIDNoLock(ctx, id)
}

func (q *FakeQuerier) GetWorkspaceAgentByInstanceID(_ context.Context, instanceID string) (database.WorkspaceAgent, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// The schema sorts this by created at, so we iterate the array backwards.
	for i := len(q.workspaceAgents) - 1; i >= 0; i-- {
		agent := q.workspaceAgents[i]
		if agent.AuthInstanceID.Valid && agent.AuthInstanceID.String == instanceID {
			return agent, nil
		}
	}
	return database.WorkspaceAgent{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceAgentLifecycleStateByID(ctx context.Context, id uuid.UUID) (database.GetWorkspaceAgentLifecycleStateByIDRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	agent, err := q.getWorkspaceAgentByIDNoLock(ctx, id)
	if err != nil {
		return database.GetWorkspaceAgentLifecycleStateByIDRow{}, err
	}
	return database.GetWorkspaceAgentLifecycleStateByIDRow{
		LifecycleState: agent.LifecycleState,
		StartedAt:      agent.StartedAt,
		ReadyAt:        agent.ReadyAt,
	}, nil
}

func (q *FakeQuerier) GetWorkspaceAgentLogsAfter(_ context.Context, arg database.GetWorkspaceAgentLogsAfterParams) ([]database.WorkspaceAgentLog, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	logs := []database.WorkspaceAgentLog{}
	for _, log := range q.workspaceAgentLogs {
		if log.AgentID != arg.AgentID {
			continue
		}
		if arg.CreatedAfter != 0 && log.ID <= arg.CreatedAfter {
			continue
		}
		logs = append(logs, log)
	}
	return logs, nil
}

func (q *FakeQuerier) GetWorkspaceAgentMetadata(_ context.Context, workspaceAgentID uuid.UUID) ([]database.WorkspaceAgentMetadatum, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	metadata := make([]database.WorkspaceAgentMetadatum, 0)
	for _, m := range q.workspaceAgentMetadata {
		if m.WorkspaceAgentID == workspaceAgentID {
			metadata = append(metadata, m)
		}
	}
	return metadata, nil
}

func (q *FakeQuerier) GetWorkspaceAgentStats(_ context.Context, createdAfter time.Time) ([]database.GetWorkspaceAgentStatsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	agentStatsCreatedAfter := make([]database.WorkspaceAgentStat, 0)
	for _, agentStat := range q.workspaceAgentStats {
		if agentStat.CreatedAt.After(createdAfter) {
			agentStatsCreatedAfter = append(agentStatsCreatedAfter, agentStat)
		}
	}

	latestAgentStats := map[uuid.UUID]database.WorkspaceAgentStat{}
	for _, agentStat := range q.workspaceAgentStats {
		if agentStat.CreatedAt.After(createdAfter) {
			latestAgentStats[agentStat.AgentID] = agentStat
		}
	}

	statByAgent := map[uuid.UUID]database.GetWorkspaceAgentStatsRow{}
	for agentID, agentStat := range latestAgentStats {
		stat := statByAgent[agentID]
		stat.AgentID = agentStat.AgentID
		stat.TemplateID = agentStat.TemplateID
		stat.UserID = agentStat.UserID
		stat.WorkspaceID = agentStat.WorkspaceID
		stat.SessionCountVSCode += agentStat.SessionCountVSCode
		stat.SessionCountJetBrains += agentStat.SessionCountJetBrains
		stat.SessionCountReconnectingPTY += agentStat.SessionCountReconnectingPTY
		stat.SessionCountSSH += agentStat.SessionCountSSH
		statByAgent[stat.AgentID] = stat
	}

	latenciesByAgent := map[uuid.UUID][]float64{}
	minimumDateByAgent := map[uuid.UUID]time.Time{}
	for _, agentStat := range agentStatsCreatedAfter {
		if agentStat.ConnectionMedianLatencyMS <= 0 {
			continue
		}
		stat := statByAgent[agentStat.AgentID]
		minimumDate := minimumDateByAgent[agentStat.AgentID]
		if agentStat.CreatedAt.Before(minimumDate) || minimumDate.IsZero() {
			minimumDateByAgent[agentStat.AgentID] = agentStat.CreatedAt
		}
		stat.WorkspaceRxBytes += agentStat.RxBytes
		stat.WorkspaceTxBytes += agentStat.TxBytes
		statByAgent[agentStat.AgentID] = stat
		latenciesByAgent[agentStat.AgentID] = append(latenciesByAgent[agentStat.AgentID], agentStat.ConnectionMedianLatencyMS)
	}

	tryPercentile := func(fs []float64, p float64) float64 {
		if len(fs) == 0 {
			return -1
		}
		sort.Float64s(fs)
		return fs[int(float64(len(fs))*p/100)]
	}

	for _, stat := range statByAgent {
		stat.AggregatedFrom = minimumDateByAgent[stat.AgentID]
		statByAgent[stat.AgentID] = stat

		latencies, ok := latenciesByAgent[stat.AgentID]
		if !ok {
			continue
		}
		stat.WorkspaceConnectionLatency50 = tryPercentile(latencies, 50)
		stat.WorkspaceConnectionLatency95 = tryPercentile(latencies, 95)
		statByAgent[stat.AgentID] = stat
	}

	stats := make([]database.GetWorkspaceAgentStatsRow, 0, len(statByAgent))
	for _, agent := range statByAgent {
		stats = append(stats, agent)
	}
	return stats, nil
}

func (q *FakeQuerier) GetWorkspaceAgentStatsAndLabels(ctx context.Context, createdAfter time.Time) ([]database.GetWorkspaceAgentStatsAndLabelsRow, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	agentStatsCreatedAfter := make([]database.WorkspaceAgentStat, 0)
	latestAgentStats := map[uuid.UUID]database.WorkspaceAgentStat{}

	for _, agentStat := range q.workspaceAgentStats {
		if agentStat.CreatedAt.After(createdAfter) {
			agentStatsCreatedAfter = append(agentStatsCreatedAfter, agentStat)
			latestAgentStats[agentStat.AgentID] = agentStat
		}
	}

	statByAgent := map[uuid.UUID]database.GetWorkspaceAgentStatsAndLabelsRow{}

	// Session and connection metrics
	for _, agentStat := range latestAgentStats {
		stat := statByAgent[agentStat.AgentID]
		stat.SessionCountVSCode += agentStat.SessionCountVSCode
		stat.SessionCountJetBrains += agentStat.SessionCountJetBrains
		stat.SessionCountReconnectingPTY += agentStat.SessionCountReconnectingPTY
		stat.SessionCountSSH += agentStat.SessionCountSSH
		stat.ConnectionCount += agentStat.ConnectionCount
		if agentStat.ConnectionMedianLatencyMS >= 0 && stat.ConnectionMedianLatencyMS < agentStat.ConnectionMedianLatencyMS {
			stat.ConnectionMedianLatencyMS = agentStat.ConnectionMedianLatencyMS
		}
		statByAgent[agentStat.AgentID] = stat
	}

	// Tx, Rx metrics
	for _, agentStat := range agentStatsCreatedAfter {
		stat := statByAgent[agentStat.AgentID]
		stat.RxBytes += agentStat.RxBytes
		stat.TxBytes += agentStat.TxBytes
		statByAgent[agentStat.AgentID] = stat
	}

	// Labels
	for _, agentStat := range agentStatsCreatedAfter {
		stat := statByAgent[agentStat.AgentID]

		user, err := q.getUserByIDNoLock(agentStat.UserID)
		if err != nil {
			return nil, err
		}

		stat.Username = user.Username

		workspace, err := q.getWorkspaceByIDNoLock(ctx, agentStat.WorkspaceID)
		if err != nil {
			return nil, err
		}
		stat.WorkspaceName = workspace.Name

		agent, err := q.getWorkspaceAgentByIDNoLock(ctx, agentStat.AgentID)
		if err != nil {
			return nil, err
		}
		stat.AgentName = agent.Name

		statByAgent[agentStat.AgentID] = stat
	}

	stats := make([]database.GetWorkspaceAgentStatsAndLabelsRow, 0, len(statByAgent))
	for _, agent := range statByAgent {
		stats = append(stats, agent)
	}
	return stats, nil
}

func (q *FakeQuerier) GetWorkspaceAgentsByResourceIDs(ctx context.Context, resourceIDs []uuid.UUID) ([]database.WorkspaceAgent, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getWorkspaceAgentsByResourceIDsNoLock(ctx, resourceIDs)
}

func (q *FakeQuerier) GetWorkspaceAgentsCreatedAfter(_ context.Context, after time.Time) ([]database.WorkspaceAgent, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	workspaceAgents := make([]database.WorkspaceAgent, 0)
	for _, agent := range q.workspaceAgents {
		if agent.CreatedAt.After(after) {
			workspaceAgents = append(workspaceAgents, agent)
		}
	}
	return workspaceAgents, nil
}

func (q *FakeQuerier) GetWorkspaceAgentsInLatestBuildByWorkspaceID(ctx context.Context, workspaceID uuid.UUID) ([]database.WorkspaceAgent, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// Get latest build for workspace.
	workspaceBuild, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, workspaceID)
	if err != nil {
		return nil, xerrors.Errorf("get latest workspace build: %w", err)
	}

	// Get resources for build.
	resources, err := q.getWorkspaceResourcesByJobIDNoLock(ctx, workspaceBuild.JobID)
	if err != nil {
		return nil, xerrors.Errorf("get workspace resources: %w", err)
	}
	if len(resources) == 0 {
		return []database.WorkspaceAgent{}, nil
	}

	resourceIDs := make([]uuid.UUID, len(resources))
	for i, resource := range resources {
		resourceIDs[i] = resource.ID
	}

	agents, err := q.getWorkspaceAgentsByResourceIDsNoLock(ctx, resourceIDs)
	if err != nil {
		return nil, xerrors.Errorf("get workspace agents: %w", err)
	}

	return agents, nil
}

func (q *FakeQuerier) GetWorkspaceAppByAgentIDAndSlug(_ context.Context, arg database.GetWorkspaceAppByAgentIDAndSlugParams) (database.WorkspaceApp, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.WorkspaceApp{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, app := range q.workspaceApps {
		if app.AgentID != arg.AgentID {
			continue
		}
		if app.Slug != arg.Slug {
			continue
		}
		return app, nil
	}
	return database.WorkspaceApp{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceAppsByAgentID(_ context.Context, id uuid.UUID) ([]database.WorkspaceApp, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	apps := make([]database.WorkspaceApp, 0)
	for _, app := range q.workspaceApps {
		if app.AgentID == id {
			apps = append(apps, app)
		}
	}
	if len(apps) == 0 {
		return nil, sql.ErrNoRows
	}
	return apps, nil
}

func (q *FakeQuerier) GetWorkspaceAppsByAgentIDs(_ context.Context, ids []uuid.UUID) ([]database.WorkspaceApp, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	apps := make([]database.WorkspaceApp, 0)
	for _, app := range q.workspaceApps {
		for _, id := range ids {
			if app.AgentID == id {
				apps = append(apps, app)
				break
			}
		}
	}
	return apps, nil
}

func (q *FakeQuerier) GetWorkspaceAppsCreatedAfter(_ context.Context, after time.Time) ([]database.WorkspaceApp, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	apps := make([]database.WorkspaceApp, 0)
	for _, app := range q.workspaceApps {
		if app.CreatedAt.After(after) {
			apps = append(apps, app)
		}
	}
	return apps, nil
}

func (q *FakeQuerier) GetWorkspaceBuildByID(ctx context.Context, id uuid.UUID) (database.WorkspaceBuild, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getWorkspaceBuildByIDNoLock(ctx, id)
}

func (q *FakeQuerier) GetWorkspaceBuildByJobID(_ context.Context, jobID uuid.UUID) (database.WorkspaceBuild, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, build := range q.workspaceBuilds {
		if build.JobID == jobID {
			return q.workspaceBuildWithUserNoLock(build), nil
		}
	}
	return database.WorkspaceBuild{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceBuildByWorkspaceIDAndBuildNumber(_ context.Context, arg database.GetWorkspaceBuildByWorkspaceIDAndBuildNumberParams) (database.WorkspaceBuild, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.WorkspaceBuild{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, workspaceBuild := range q.workspaceBuilds {
		if workspaceBuild.WorkspaceID != arg.WorkspaceID {
			continue
		}
		if workspaceBuild.BuildNumber != arg.BuildNumber {
			continue
		}
		return q.workspaceBuildWithUserNoLock(workspaceBuild), nil
	}
	return database.WorkspaceBuild{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceBuildParameters(_ context.Context, workspaceBuildID uuid.UUID) ([]database.WorkspaceBuildParameter, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	params := make([]database.WorkspaceBuildParameter, 0)
	for _, param := range q.workspaceBuildParameters {
		if param.WorkspaceBuildID != workspaceBuildID {
			continue
		}
		params = append(params, param)
	}
	return params, nil
}

func (q *FakeQuerier) GetWorkspaceBuildsByWorkspaceID(_ context.Context,
	params database.GetWorkspaceBuildsByWorkspaceIDParams,
) ([]database.WorkspaceBuild, error) {
	if err := validateDatabaseType(params); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	history := make([]database.WorkspaceBuild, 0)
	for _, workspaceBuild := range q.workspaceBuilds {
		if workspaceBuild.CreatedAt.Before(params.Since) {
			continue
		}
		if workspaceBuild.WorkspaceID == params.WorkspaceID {
			history = append(history, q.workspaceBuildWithUserNoLock(workspaceBuild))
		}
	}

	// Order by build_number
	slices.SortFunc(history, func(a, b database.WorkspaceBuild) bool {
		// use greater than since we want descending order
		return a.BuildNumber > b.BuildNumber
	})

	if params.AfterID != uuid.Nil {
		found := false
		for i, v := range history {
			if v.ID == params.AfterID {
				// We want to return all builds after index i.
				history = history[i+1:]
				found = true
				break
			}
		}

		// If no builds after the time, then we return an empty list.
		if !found {
			return nil, sql.ErrNoRows
		}
	}

	if params.OffsetOpt > 0 {
		if int(params.OffsetOpt) > len(history)-1 {
			return nil, sql.ErrNoRows
		}
		history = history[params.OffsetOpt:]
	}

	if params.LimitOpt > 0 {
		if int(params.LimitOpt) > len(history) {
			params.LimitOpt = int32(len(history))
		}
		history = history[:params.LimitOpt]
	}

	if len(history) == 0 {
		return nil, sql.ErrNoRows
	}
	return history, nil
}

func (q *FakeQuerier) GetWorkspaceBuildsCreatedAfter(_ context.Context, after time.Time) ([]database.WorkspaceBuild, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	workspaceBuilds := make([]database.WorkspaceBuild, 0)
	for _, workspaceBuild := range q.workspaceBuilds {
		if workspaceBuild.CreatedAt.After(after) {
			workspaceBuilds = append(workspaceBuilds, q.workspaceBuildWithUserNoLock(workspaceBuild))
		}
	}
	return workspaceBuilds, nil
}

func (q *FakeQuerier) GetWorkspaceByAgentID(ctx context.Context, agentID uuid.UUID) (database.Workspace, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getWorkspaceByAgentIDNoLock(ctx, agentID)
}

func (q *FakeQuerier) GetWorkspaceByID(ctx context.Context, id uuid.UUID) (database.Workspace, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getWorkspaceByIDNoLock(ctx, id)
}

func (q *FakeQuerier) GetWorkspaceByOwnerIDAndName(_ context.Context, arg database.GetWorkspaceByOwnerIDAndNameParams) (database.Workspace, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Workspace{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var found *database.Workspace
	for _, workspace := range q.workspaces {
		workspace := workspace
		if workspace.OwnerID != arg.OwnerID {
			continue
		}
		if !strings.EqualFold(workspace.Name, arg.Name) {
			continue
		}
		if workspace.Deleted != arg.Deleted {
			continue
		}

		// Return the most recent workspace with the given name
		if found == nil || workspace.CreatedAt.After(found.CreatedAt) {
			found = &workspace
		}
	}
	if found != nil {
		return *found, nil
	}
	return database.Workspace{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceByWorkspaceAppID(_ context.Context, workspaceAppID uuid.UUID) (database.Workspace, error) {
	if err := validateDatabaseType(workspaceAppID); err != nil {
		return database.Workspace{}, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, workspaceApp := range q.workspaceApps {
		workspaceApp := workspaceApp
		if workspaceApp.ID == workspaceAppID {
			return q.getWorkspaceByAgentIDNoLock(context.Background(), workspaceApp.AgentID)
		}
	}
	return database.Workspace{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceProxies(_ context.Context) ([]database.WorkspaceProxy, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	cpy := make([]database.WorkspaceProxy, 0, len(q.workspaceProxies))

	for _, p := range q.workspaceProxies {
		if !p.Deleted {
			cpy = append(cpy, p)
		}
	}
	return cpy, nil
}

func (q *FakeQuerier) GetWorkspaceProxyByHostname(_ context.Context, params database.GetWorkspaceProxyByHostnameParams) (database.WorkspaceProxy, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// Return zero rows if this is called with a non-sanitized hostname. The SQL
	// version of this query does the same thing.
	if !validProxyByHostnameRegex.MatchString(params.Hostname) {
		return database.WorkspaceProxy{}, sql.ErrNoRows
	}

	// This regex matches the SQL version.
	accessURLRegex := regexp.MustCompile(`[^:]*://` + regexp.QuoteMeta(params.Hostname) + `([:/]?.)*`)

	for _, proxy := range q.workspaceProxies {
		if proxy.Deleted {
			continue
		}
		if params.AllowAccessUrl && accessURLRegex.MatchString(proxy.Url) {
			return proxy, nil
		}

		// Compile the app hostname regex. This is slow sadly.
		if params.AllowWildcardHostname {
			wildcardRegexp, err := httpapi.CompileHostnamePattern(proxy.WildcardHostname)
			if err != nil {
				return database.WorkspaceProxy{}, xerrors.Errorf("compile hostname pattern %q for proxy %q (%s): %w", proxy.WildcardHostname, proxy.Name, proxy.ID.String(), err)
			}
			if _, ok := httpapi.ExecuteHostnamePattern(wildcardRegexp, params.Hostname); ok {
				return proxy, nil
			}
		}
	}

	return database.WorkspaceProxy{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceProxyByID(_ context.Context, id uuid.UUID) (database.WorkspaceProxy, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, proxy := range q.workspaceProxies {
		if proxy.ID == id {
			return proxy, nil
		}
	}
	return database.WorkspaceProxy{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceProxyByName(_ context.Context, name string) (database.WorkspaceProxy, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for _, proxy := range q.workspaceProxies {
		if proxy.Deleted {
			continue
		}
		if proxy.Name == name {
			return proxy, nil
		}
	}
	return database.WorkspaceProxy{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceResourceByID(_ context.Context, id uuid.UUID) (database.WorkspaceResource, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, resource := range q.workspaceResources {
		if resource.ID == id {
			return resource, nil
		}
	}
	return database.WorkspaceResource{}, sql.ErrNoRows
}

func (q *FakeQuerier) GetWorkspaceResourceMetadataByResourceIDs(_ context.Context, ids []uuid.UUID) ([]database.WorkspaceResourceMetadatum, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	metadata := make([]database.WorkspaceResourceMetadatum, 0)
	for _, metadatum := range q.workspaceResourceMetadata {
		for _, id := range ids {
			if metadatum.WorkspaceResourceID == id {
				metadata = append(metadata, metadatum)
			}
		}
	}
	return metadata, nil
}

func (q *FakeQuerier) GetWorkspaceResourceMetadataCreatedAfter(ctx context.Context, after time.Time) ([]database.WorkspaceResourceMetadatum, error) {
	resources, err := q.GetWorkspaceResourcesCreatedAfter(ctx, after)
	if err != nil {
		return nil, err
	}
	resourceIDs := map[uuid.UUID]struct{}{}
	for _, resource := range resources {
		resourceIDs[resource.ID] = struct{}{}
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	metadata := make([]database.WorkspaceResourceMetadatum, 0)
	for _, m := range q.workspaceResourceMetadata {
		_, ok := resourceIDs[m.WorkspaceResourceID]
		if !ok {
			continue
		}
		metadata = append(metadata, m)
	}
	return metadata, nil
}

func (q *FakeQuerier) GetWorkspaceResourcesByJobID(ctx context.Context, jobID uuid.UUID) ([]database.WorkspaceResource, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	return q.getWorkspaceResourcesByJobIDNoLock(ctx, jobID)
}

func (q *FakeQuerier) GetWorkspaceResourcesByJobIDs(_ context.Context, jobIDs []uuid.UUID) ([]database.WorkspaceResource, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	resources := make([]database.WorkspaceResource, 0)
	for _, resource := range q.workspaceResources {
		for _, jobID := range jobIDs {
			if resource.JobID != jobID {
				continue
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (q *FakeQuerier) GetWorkspaceResourcesCreatedAfter(_ context.Context, after time.Time) ([]database.WorkspaceResource, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	resources := make([]database.WorkspaceResource, 0)
	for _, resource := range q.workspaceResources {
		if resource.CreatedAt.After(after) {
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (q *FakeQuerier) GetWorkspaces(ctx context.Context, arg database.GetWorkspacesParams) ([]database.GetWorkspacesRow, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	// A nil auth filter means no auth filter.
	workspaceRows, err := q.GetAuthorizedWorkspaces(ctx, arg, nil)
	return workspaceRows, err
}

func (q *FakeQuerier) GetWorkspacesEligibleForTransition(ctx context.Context, now time.Time) ([]database.Workspace, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	workspaces := []database.Workspace{}
	for _, workspace := range q.workspaces {
		build, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, workspace.ID)
		if err != nil {
			return nil, err
		}

		if build.Transition == database.WorkspaceTransitionStart &&
			!build.Deadline.IsZero() &&
			build.Deadline.Before(now) &&
			!workspace.LockedAt.Valid {
			workspaces = append(workspaces, workspace)
			continue
		}

		if build.Transition == database.WorkspaceTransitionStop &&
			workspace.AutostartSchedule.Valid &&
			!workspace.LockedAt.Valid {
			workspaces = append(workspaces, workspace)
			continue
		}

		job, err := q.getProvisionerJobByIDNoLock(ctx, build.JobID)
		if err != nil {
			return nil, xerrors.Errorf("get provisioner job by ID: %w", err)
		}
		if db2sdk.ProvisionerJobStatus(job) == codersdk.ProvisionerJobFailed {
			workspaces = append(workspaces, workspace)
			continue
		}

		template, err := q.GetTemplateByID(ctx, workspace.TemplateID)
		if err != nil {
			return nil, xerrors.Errorf("get template by ID: %w", err)
		}
		if !workspace.LockedAt.Valid && template.InactivityTTL > 0 {
			workspaces = append(workspaces, workspace)
			continue
		}
		if workspace.LockedAt.Valid && template.LockedTTL > 0 {
			workspaces = append(workspaces, workspace)
			continue
		}
	}

	return workspaces, nil
}

func (q *FakeQuerier) InsertAPIKey(_ context.Context, arg database.InsertAPIKeyParams) (database.APIKey, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.APIKey{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	if arg.LifetimeSeconds == 0 {
		arg.LifetimeSeconds = 86400
	}

	for _, u := range q.users {
		if u.ID == arg.UserID && u.Deleted {
			return database.APIKey{}, xerrors.Errorf("refusing to create APIKey for deleted user")
		}
	}

	//nolint:gosimple
	key := database.APIKey{
		ID:              arg.ID,
		LifetimeSeconds: arg.LifetimeSeconds,
		HashedSecret:    arg.HashedSecret,
		IPAddress:       arg.IPAddress,
		UserID:          arg.UserID,
		ExpiresAt:       arg.ExpiresAt,
		CreatedAt:       arg.CreatedAt,
		UpdatedAt:       arg.UpdatedAt,
		LastUsed:        arg.LastUsed,
		LoginType:       arg.LoginType,
		Scope:           arg.Scope,
		TokenName:       arg.TokenName,
	}
	q.apiKeys = append(q.apiKeys, key)
	return key, nil
}

func (q *FakeQuerier) InsertAllUsersGroup(ctx context.Context, orgID uuid.UUID) (database.Group, error) {
	return q.InsertGroup(ctx, database.InsertGroupParams{
		ID:             orgID,
		Name:           database.AllUsersGroup,
		DisplayName:    "",
		OrganizationID: orgID,
	})
}

func (q *FakeQuerier) InsertAuditLog(_ context.Context, arg database.InsertAuditLogParams) (database.AuditLog, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.AuditLog{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	alog := database.AuditLog(arg)

	q.auditLogs = append(q.auditLogs, alog)
	slices.SortFunc(q.auditLogs, func(a, b database.AuditLog) bool {
		return a.Time.Before(b.Time)
	})

	return alog, nil
}

func (q *FakeQuerier) InsertDERPMeshKey(_ context.Context, id string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.derpMeshKey = id
	return nil
}

func (q *FakeQuerier) InsertDeploymentID(_ context.Context, id string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.deploymentID = id
	return nil
}

func (q *FakeQuerier) InsertFile(_ context.Context, arg database.InsertFileParams) (database.File, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.File{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	file := database.File{
		ID:        arg.ID,
		Hash:      arg.Hash,
		CreatedAt: arg.CreatedAt,
		CreatedBy: arg.CreatedBy,
		Mimetype:  arg.Mimetype,
		Data:      arg.Data,
	}
	q.files = append(q.files, file)
	return file, nil
}

func (q *FakeQuerier) InsertGitAuthLink(_ context.Context, arg database.InsertGitAuthLinkParams) (database.GitAuthLink, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.GitAuthLink{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()
	// nolint:gosimple
	gitAuthLink := database.GitAuthLink{
		ProviderID:        arg.ProviderID,
		UserID:            arg.UserID,
		CreatedAt:         arg.CreatedAt,
		UpdatedAt:         arg.UpdatedAt,
		OAuthAccessToken:  arg.OAuthAccessToken,
		OAuthRefreshToken: arg.OAuthRefreshToken,
		OAuthExpiry:       arg.OAuthExpiry,
	}
	q.gitAuthLinks = append(q.gitAuthLinks, gitAuthLink)
	return gitAuthLink, nil
}

func (q *FakeQuerier) InsertGitSSHKey(_ context.Context, arg database.InsertGitSSHKeyParams) (database.GitSSHKey, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.GitSSHKey{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	gitSSHKey := database.GitSSHKey{
		UserID:     arg.UserID,
		CreatedAt:  arg.CreatedAt,
		UpdatedAt:  arg.UpdatedAt,
		PrivateKey: arg.PrivateKey,
		PublicKey:  arg.PublicKey,
	}
	q.gitSSHKey = append(q.gitSSHKey, gitSSHKey)
	return gitSSHKey, nil
}

func (q *FakeQuerier) InsertGroup(_ context.Context, arg database.InsertGroupParams) (database.Group, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Group{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for _, group := range q.groups {
		if group.OrganizationID == arg.OrganizationID &&
			group.Name == arg.Name {
			return database.Group{}, errDuplicateKey
		}
	}

	//nolint:gosimple
	group := database.Group{
		ID:             arg.ID,
		Name:           arg.Name,
		DisplayName:    arg.DisplayName,
		OrganizationID: arg.OrganizationID,
		AvatarURL:      arg.AvatarURL,
		QuotaAllowance: arg.QuotaAllowance,
	}

	q.groups = append(q.groups, group)

	return group, nil
}

func (q *FakeQuerier) InsertGroupMember(_ context.Context, arg database.InsertGroupMemberParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for _, member := range q.groupMembers {
		if member.GroupID == arg.GroupID &&
			member.UserID == arg.UserID {
			return errDuplicateKey
		}
	}

	//nolint:gosimple
	q.groupMembers = append(q.groupMembers, database.GroupMember{
		GroupID: arg.GroupID,
		UserID:  arg.UserID,
	})

	return nil
}

func (q *FakeQuerier) InsertLicense(
	_ context.Context, arg database.InsertLicenseParams,
) (database.License, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.License{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	l := database.License{
		ID:         q.lastLicenseID + 1,
		UploadedAt: arg.UploadedAt,
		JWT:        arg.JWT,
		Exp:        arg.Exp,
	}
	q.lastLicenseID = l.ID
	q.licenses = append(q.licenses, l)
	return l, nil
}

func (q *FakeQuerier) InsertOrganization(_ context.Context, arg database.InsertOrganizationParams) (database.Organization, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Organization{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	organization := database.Organization{
		ID:        arg.ID,
		Name:      arg.Name,
		CreatedAt: arg.CreatedAt,
		UpdatedAt: arg.UpdatedAt,
	}
	q.organizations = append(q.organizations, organization)
	return organization, nil
}

func (q *FakeQuerier) InsertOrganizationMember(_ context.Context, arg database.InsertOrganizationMemberParams) (database.OrganizationMember, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.OrganizationMember{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	organizationMember := database.OrganizationMember{
		OrganizationID: arg.OrganizationID,
		UserID:         arg.UserID,
		CreatedAt:      arg.CreatedAt,
		UpdatedAt:      arg.UpdatedAt,
		Roles:          arg.Roles,
	}
	q.organizationMembers = append(q.organizationMembers, organizationMember)
	return organizationMember, nil
}

func (q *FakeQuerier) InsertProvisionerDaemon(_ context.Context, arg database.InsertProvisionerDaemonParams) (database.ProvisionerDaemon, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.ProvisionerDaemon{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	daemon := database.ProvisionerDaemon{
		ID:           arg.ID,
		CreatedAt:    arg.CreatedAt,
		Name:         arg.Name,
		Provisioners: arg.Provisioners,
		Tags:         arg.Tags,
	}
	q.provisionerDaemons = append(q.provisionerDaemons, daemon)
	return daemon, nil
}

func (q *FakeQuerier) InsertProvisionerJob(_ context.Context, arg database.InsertProvisionerJobParams) (database.ProvisionerJob, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.ProvisionerJob{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	job := database.ProvisionerJob{
		ID:             arg.ID,
		CreatedAt:      arg.CreatedAt,
		UpdatedAt:      arg.UpdatedAt,
		OrganizationID: arg.OrganizationID,
		InitiatorID:    arg.InitiatorID,
		Provisioner:    arg.Provisioner,
		StorageMethod:  arg.StorageMethod,
		FileID:         arg.FileID,
		Type:           arg.Type,
		Input:          arg.Input,
		Tags:           arg.Tags,
	}
	q.provisionerJobs = append(q.provisionerJobs, job)
	return job, nil
}

func (q *FakeQuerier) InsertProvisionerJobLogs(_ context.Context, arg database.InsertProvisionerJobLogsParams) ([]database.ProvisionerJobLog, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	logs := make([]database.ProvisionerJobLog, 0)
	id := int64(1)
	if len(q.provisionerJobLogs) > 0 {
		id = q.provisionerJobLogs[len(q.provisionerJobLogs)-1].ID
	}
	for index, output := range arg.Output {
		id++
		logs = append(logs, database.ProvisionerJobLog{
			ID:        id,
			JobID:     arg.JobID,
			CreatedAt: arg.CreatedAt[index],
			Source:    arg.Source[index],
			Level:     arg.Level[index],
			Stage:     arg.Stage[index],
			Output:    output,
		})
	}
	q.provisionerJobLogs = append(q.provisionerJobLogs, logs...)
	return logs, nil
}

func (q *FakeQuerier) InsertReplica(_ context.Context, arg database.InsertReplicaParams) (database.Replica, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Replica{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	replica := database.Replica{
		ID:              arg.ID,
		CreatedAt:       arg.CreatedAt,
		StartedAt:       arg.StartedAt,
		UpdatedAt:       arg.UpdatedAt,
		Hostname:        arg.Hostname,
		RegionID:        arg.RegionID,
		RelayAddress:    arg.RelayAddress,
		Version:         arg.Version,
		DatabaseLatency: arg.DatabaseLatency,
		Primary:         arg.Primary,
	}
	q.replicas = append(q.replicas, replica)
	return replica, nil
}

func (q *FakeQuerier) InsertTemplate(_ context.Context, arg database.InsertTemplateParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	template := database.TemplateTable{
		ID:                           arg.ID,
		CreatedAt:                    arg.CreatedAt,
		UpdatedAt:                    arg.UpdatedAt,
		OrganizationID:               arg.OrganizationID,
		Name:                         arg.Name,
		Provisioner:                  arg.Provisioner,
		ActiveVersionID:              arg.ActiveVersionID,
		Description:                  arg.Description,
		CreatedBy:                    arg.CreatedBy,
		UserACL:                      arg.UserACL,
		GroupACL:                     arg.GroupACL,
		DisplayName:                  arg.DisplayName,
		Icon:                         arg.Icon,
		AllowUserCancelWorkspaceJobs: arg.AllowUserCancelWorkspaceJobs,
		AllowUserAutostart:           true,
		AllowUserAutostop:            true,
	}
	q.templates = append(q.templates, template)
	return nil
}

func (q *FakeQuerier) InsertTemplateVersion(_ context.Context, arg database.InsertTemplateVersionParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	if len(arg.Message) > 1048576 {
		return xerrors.New("message too long")
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	version := database.TemplateVersionTable{
		ID:             arg.ID,
		TemplateID:     arg.TemplateID,
		OrganizationID: arg.OrganizationID,
		CreatedAt:      arg.CreatedAt,
		UpdatedAt:      arg.UpdatedAt,
		Name:           arg.Name,
		Message:        arg.Message,
		Readme:         arg.Readme,
		JobID:          arg.JobID,
		CreatedBy:      arg.CreatedBy,
	}
	q.templateVersions = append(q.templateVersions, version)
	return nil
}

func (q *FakeQuerier) InsertTemplateVersionParameter(_ context.Context, arg database.InsertTemplateVersionParameterParams) (database.TemplateVersionParameter, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.TemplateVersionParameter{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	param := database.TemplateVersionParameter{
		TemplateVersionID:   arg.TemplateVersionID,
		Name:                arg.Name,
		DisplayName:         arg.DisplayName,
		Description:         arg.Description,
		Type:                arg.Type,
		Mutable:             arg.Mutable,
		DefaultValue:        arg.DefaultValue,
		Icon:                arg.Icon,
		Options:             arg.Options,
		ValidationError:     arg.ValidationError,
		ValidationRegex:     arg.ValidationRegex,
		ValidationMin:       arg.ValidationMin,
		ValidationMax:       arg.ValidationMax,
		ValidationMonotonic: arg.ValidationMonotonic,
		Required:            arg.Required,
		DisplayOrder:        arg.DisplayOrder,
		Ephemeral:           arg.Ephemeral,
	}
	q.templateVersionParameters = append(q.templateVersionParameters, param)
	return param, nil
}

func (q *FakeQuerier) InsertTemplateVersionVariable(_ context.Context, arg database.InsertTemplateVersionVariableParams) (database.TemplateVersionVariable, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.TemplateVersionVariable{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	variable := database.TemplateVersionVariable{
		TemplateVersionID: arg.TemplateVersionID,
		Name:              arg.Name,
		Description:       arg.Description,
		Type:              arg.Type,
		Value:             arg.Value,
		DefaultValue:      arg.DefaultValue,
		Required:          arg.Required,
		Sensitive:         arg.Sensitive,
	}
	q.templateVersionVariables = append(q.templateVersionVariables, variable)
	return variable, nil
}

func (q *FakeQuerier) InsertUser(_ context.Context, arg database.InsertUserParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	// There is a common bug when using dbfake that 2 inserted users have the
	// same created_at time. This causes user order to not be deterministic,
	// which breaks some unit tests.
	// To fix this, we make sure that the created_at time is always greater
	// than the last user's created_at time.
	allUsers, _ := q.GetUsers(context.Background(), database.GetUsersParams{})
	if len(allUsers) > 0 {
		lastUser := allUsers[len(allUsers)-1]
		if arg.CreatedAt.Before(lastUser.CreatedAt) ||
			arg.CreatedAt.Equal(lastUser.CreatedAt) {
			// 1 ms is a good enough buffer.
			arg.CreatedAt = lastUser.CreatedAt.Add(time.Millisecond)
		}
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for _, user := range q.users {
		if user.Username == arg.Username && !user.Deleted {
			return database.User{}, errDuplicateKey
		}
	}

	user := database.User{
		ID:             arg.ID,
		Email:          arg.Email,
		HashedPassword: arg.HashedPassword,
		CreatedAt:      arg.CreatedAt,
		UpdatedAt:      arg.UpdatedAt,
		Username:       arg.Username,
		Status:         database.UserStatusDormant,
		RBACRoles:      arg.RBACRoles,
		LoginType:      arg.LoginType,
	}
	q.users = append(q.users, user)
	return user, nil
}

func (q *FakeQuerier) InsertUserGroupsByName(_ context.Context, arg database.InsertUserGroupsByNameParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	var groupIDs []uuid.UUID
	for _, group := range q.groups {
		for _, groupName := range arg.GroupNames {
			if group.Name == groupName {
				groupIDs = append(groupIDs, group.ID)
			}
		}
	}

	for _, groupID := range groupIDs {
		q.groupMembers = append(q.groupMembers, database.GroupMember{
			UserID:  arg.UserID,
			GroupID: groupID,
		})
	}

	return nil
}

func (q *FakeQuerier) InsertUserLink(_ context.Context, args database.InsertUserLinkParams) (database.UserLink, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	link := database.UserLink{
		UserID:            args.UserID,
		LoginType:         args.LoginType,
		LinkedID:          args.LinkedID,
		OAuthAccessToken:  args.OAuthAccessToken,
		OAuthRefreshToken: args.OAuthRefreshToken,
		OAuthExpiry:       args.OAuthExpiry,
	}

	q.userLinks = append(q.userLinks, link)

	return link, nil
}

func (q *FakeQuerier) InsertWorkspace(_ context.Context, arg database.InsertWorkspaceParams) (database.Workspace, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Workspace{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	workspace := database.Workspace{
		ID:                arg.ID,
		CreatedAt:         arg.CreatedAt,
		UpdatedAt:         arg.UpdatedAt,
		OwnerID:           arg.OwnerID,
		OrganizationID:    arg.OrganizationID,
		TemplateID:        arg.TemplateID,
		Name:              arg.Name,
		AutostartSchedule: arg.AutostartSchedule,
		Ttl:               arg.Ttl,
		LastUsedAt:        arg.LastUsedAt,
	}
	q.workspaces = append(q.workspaces, workspace)
	return workspace, nil
}

func (q *FakeQuerier) InsertWorkspaceAgent(_ context.Context, arg database.InsertWorkspaceAgentParams) (database.WorkspaceAgent, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.WorkspaceAgent{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	agent := database.WorkspaceAgent{
		ID:                       arg.ID,
		CreatedAt:                arg.CreatedAt,
		UpdatedAt:                arg.UpdatedAt,
		ResourceID:               arg.ResourceID,
		AuthToken:                arg.AuthToken,
		AuthInstanceID:           arg.AuthInstanceID,
		EnvironmentVariables:     arg.EnvironmentVariables,
		Name:                     arg.Name,
		Architecture:             arg.Architecture,
		OperatingSystem:          arg.OperatingSystem,
		Directory:                arg.Directory,
		StartupScriptBehavior:    arg.StartupScriptBehavior,
		StartupScript:            arg.StartupScript,
		InstanceMetadata:         arg.InstanceMetadata,
		ResourceMetadata:         arg.ResourceMetadata,
		ConnectionTimeoutSeconds: arg.ConnectionTimeoutSeconds,
		TroubleshootingURL:       arg.TroubleshootingURL,
		MOTDFile:                 arg.MOTDFile,
		LifecycleState:           database.WorkspaceAgentLifecycleStateCreated,
		ShutdownScript:           arg.ShutdownScript,
	}

	q.workspaceAgents = append(q.workspaceAgents, agent)
	return agent, nil
}

func (q *FakeQuerier) InsertWorkspaceAgentLogs(_ context.Context, arg database.InsertWorkspaceAgentLogsParams) ([]database.WorkspaceAgentLog, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	logs := []database.WorkspaceAgentLog{}
	id := int64(0)
	if len(q.workspaceAgentLogs) > 0 {
		id = q.workspaceAgentLogs[len(q.workspaceAgentLogs)-1].ID
	}
	outputLength := int32(0)
	for index, output := range arg.Output {
		id++
		logs = append(logs, database.WorkspaceAgentLog{
			ID:        id,
			AgentID:   arg.AgentID,
			CreatedAt: arg.CreatedAt[index],
			Level:     arg.Level[index],
			Source:    arg.Source[index],
			Output:    output,
		})
		outputLength += int32(len(output))
	}
	for index, agent := range q.workspaceAgents {
		if agent.ID != arg.AgentID {
			continue
		}
		// Greater than 1MB, same as the PostgreSQL constraint!
		if agent.LogsLength+outputLength > (1 << 20) {
			return nil, &pq.Error{
				Constraint: "max_logs_length",
				Table:      "workspace_agents",
			}
		}
		agent.LogsLength += outputLength
		q.workspaceAgents[index] = agent
		break
	}
	q.workspaceAgentLogs = append(q.workspaceAgentLogs, logs...)
	return logs, nil
}

func (q *FakeQuerier) InsertWorkspaceAgentMetadata(_ context.Context, arg database.InsertWorkspaceAgentMetadataParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	metadatum := database.WorkspaceAgentMetadatum{
		WorkspaceAgentID: arg.WorkspaceAgentID,
		Script:           arg.Script,
		DisplayName:      arg.DisplayName,
		Key:              arg.Key,
		Timeout:          arg.Timeout,
		Interval:         arg.Interval,
	}

	q.workspaceAgentMetadata = append(q.workspaceAgentMetadata, metadatum)
	return nil
}

func (q *FakeQuerier) InsertWorkspaceAgentStat(_ context.Context, p database.InsertWorkspaceAgentStatParams) (database.WorkspaceAgentStat, error) {
	if err := validateDatabaseType(p); err != nil {
		return database.WorkspaceAgentStat{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	stat := database.WorkspaceAgentStat{
		ID:                          p.ID,
		CreatedAt:                   p.CreatedAt,
		WorkspaceID:                 p.WorkspaceID,
		AgentID:                     p.AgentID,
		UserID:                      p.UserID,
		ConnectionsByProto:          p.ConnectionsByProto,
		ConnectionCount:             p.ConnectionCount,
		RxPackets:                   p.RxPackets,
		RxBytes:                     p.RxBytes,
		TxPackets:                   p.TxPackets,
		TxBytes:                     p.TxBytes,
		TemplateID:                  p.TemplateID,
		SessionCountVSCode:          p.SessionCountVSCode,
		SessionCountJetBrains:       p.SessionCountJetBrains,
		SessionCountReconnectingPTY: p.SessionCountReconnectingPTY,
		SessionCountSSH:             p.SessionCountSSH,
		ConnectionMedianLatencyMS:   p.ConnectionMedianLatencyMS,
	}
	q.workspaceAgentStats = append(q.workspaceAgentStats, stat)
	return stat, nil
}

func (q *FakeQuerier) InsertWorkspaceAgentStats(_ context.Context, arg database.InsertWorkspaceAgentStatsParams) error {
	err := validateDatabaseType(arg)
	if err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	var connectionsByProto []map[string]int64
	if err := json.Unmarshal(arg.ConnectionsByProto, &connectionsByProto); err != nil {
		return err
	}
	for i := 0; i < len(arg.ID); i++ {
		cbp, err := json.Marshal(connectionsByProto[i])
		if err != nil {
			return xerrors.Errorf("failed to marshal connections_by_proto: %w", err)
		}
		stat := database.WorkspaceAgentStat{
			ID:                          arg.ID[i],
			CreatedAt:                   arg.CreatedAt[i],
			WorkspaceID:                 arg.WorkspaceID[i],
			AgentID:                     arg.AgentID[i],
			UserID:                      arg.UserID[i],
			ConnectionsByProto:          cbp,
			ConnectionCount:             arg.ConnectionCount[i],
			RxPackets:                   arg.RxPackets[i],
			RxBytes:                     arg.RxBytes[i],
			TxPackets:                   arg.TxPackets[i],
			TxBytes:                     arg.TxBytes[i],
			TemplateID:                  arg.TemplateID[i],
			SessionCountVSCode:          arg.SessionCountVSCode[i],
			SessionCountJetBrains:       arg.SessionCountJetBrains[i],
			SessionCountReconnectingPTY: arg.SessionCountReconnectingPTY[i],
			SessionCountSSH:             arg.SessionCountSSH[i],
			ConnectionMedianLatencyMS:   arg.ConnectionMedianLatencyMS[i],
		}
		q.workspaceAgentStats = append(q.workspaceAgentStats, stat)
	}

	return nil
}

func (q *FakeQuerier) InsertWorkspaceApp(_ context.Context, arg database.InsertWorkspaceAppParams) (database.WorkspaceApp, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.WorkspaceApp{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	if arg.SharingLevel == "" {
		arg.SharingLevel = database.AppSharingLevelOwner
	}

	// nolint:gosimple
	workspaceApp := database.WorkspaceApp{
		ID:                   arg.ID,
		AgentID:              arg.AgentID,
		CreatedAt:            arg.CreatedAt,
		Slug:                 arg.Slug,
		DisplayName:          arg.DisplayName,
		Icon:                 arg.Icon,
		Command:              arg.Command,
		Url:                  arg.Url,
		External:             arg.External,
		Subdomain:            arg.Subdomain,
		SharingLevel:         arg.SharingLevel,
		HealthcheckUrl:       arg.HealthcheckUrl,
		HealthcheckInterval:  arg.HealthcheckInterval,
		HealthcheckThreshold: arg.HealthcheckThreshold,
		Health:               arg.Health,
	}
	q.workspaceApps = append(q.workspaceApps, workspaceApp)
	return workspaceApp, nil
}

func (q *FakeQuerier) InsertWorkspaceBuild(_ context.Context, arg database.InsertWorkspaceBuildParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	workspaceBuild := database.WorkspaceBuildTable{
		ID:                arg.ID,
		CreatedAt:         arg.CreatedAt,
		UpdatedAt:         arg.UpdatedAt,
		WorkspaceID:       arg.WorkspaceID,
		TemplateVersionID: arg.TemplateVersionID,
		BuildNumber:       arg.BuildNumber,
		Transition:        arg.Transition,
		InitiatorID:       arg.InitiatorID,
		JobID:             arg.JobID,
		ProvisionerState:  arg.ProvisionerState,
		Deadline:          arg.Deadline,
		Reason:            arg.Reason,
	}
	q.workspaceBuilds = append(q.workspaceBuilds, workspaceBuild)
	return nil
}

func (q *FakeQuerier) InsertWorkspaceBuildParameters(_ context.Context, arg database.InsertWorkspaceBuildParametersParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, name := range arg.Name {
		q.workspaceBuildParameters = append(q.workspaceBuildParameters, database.WorkspaceBuildParameter{
			WorkspaceBuildID: arg.WorkspaceBuildID,
			Name:             name,
			Value:            arg.Value[index],
		})
	}
	return nil
}

func (q *FakeQuerier) InsertWorkspaceProxy(_ context.Context, arg database.InsertWorkspaceProxyParams) (database.WorkspaceProxy, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	lastRegionID := int32(0)
	for _, p := range q.workspaceProxies {
		if !p.Deleted && p.Name == arg.Name {
			return database.WorkspaceProxy{}, errDuplicateKey
		}
		if p.RegionID > lastRegionID {
			lastRegionID = p.RegionID
		}
	}

	p := database.WorkspaceProxy{
		ID:                arg.ID,
		Name:              arg.Name,
		DisplayName:       arg.DisplayName,
		Icon:              arg.Icon,
		DerpEnabled:       arg.DerpEnabled,
		DerpOnly:          arg.DerpOnly,
		TokenHashedSecret: arg.TokenHashedSecret,
		RegionID:          lastRegionID + 1,
		CreatedAt:         arg.CreatedAt,
		UpdatedAt:         arg.UpdatedAt,
		Deleted:           false,
	}
	q.workspaceProxies = append(q.workspaceProxies, p)
	return p, nil
}

func (q *FakeQuerier) InsertWorkspaceResource(_ context.Context, arg database.InsertWorkspaceResourceParams) (database.WorkspaceResource, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.WorkspaceResource{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	resource := database.WorkspaceResource{
		ID:         arg.ID,
		CreatedAt:  arg.CreatedAt,
		JobID:      arg.JobID,
		Transition: arg.Transition,
		Type:       arg.Type,
		Name:       arg.Name,
		Hide:       arg.Hide,
		Icon:       arg.Icon,
		DailyCost:  arg.DailyCost,
	}
	q.workspaceResources = append(q.workspaceResources, resource)
	return resource, nil
}

func (q *FakeQuerier) InsertWorkspaceResourceMetadata(_ context.Context, arg database.InsertWorkspaceResourceMetadataParams) ([]database.WorkspaceResourceMetadatum, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	metadata := make([]database.WorkspaceResourceMetadatum, 0)
	id := int64(1)
	if len(q.workspaceResourceMetadata) > 0 {
		id = q.workspaceResourceMetadata[len(q.workspaceResourceMetadata)-1].ID
	}
	for index, key := range arg.Key {
		id++
		value := arg.Value[index]
		metadata = append(metadata, database.WorkspaceResourceMetadatum{
			ID:                  id,
			WorkspaceResourceID: arg.WorkspaceResourceID,
			Key:                 key,
			Value: sql.NullString{
				String: value,
				Valid:  value != "",
			},
			Sensitive: arg.Sensitive[index],
		})
	}
	q.workspaceResourceMetadata = append(q.workspaceResourceMetadata, metadata...)
	return metadata, nil
}

func (q *FakeQuerier) RegisterWorkspaceProxy(_ context.Context, arg database.RegisterWorkspaceProxyParams) (database.WorkspaceProxy, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, p := range q.workspaceProxies {
		if p.ID == arg.ID {
			p.Url = arg.Url
			p.WildcardHostname = arg.WildcardHostname
			p.DerpEnabled = arg.DerpEnabled
			p.DerpOnly = arg.DerpOnly
			p.UpdatedAt = database.Now()
			q.workspaceProxies[i] = p
			return p, nil
		}
	}
	return database.WorkspaceProxy{}, sql.ErrNoRows
}

func (*FakeQuerier) TryAcquireLock(_ context.Context, _ int64) (bool, error) {
	return false, xerrors.New("TryAcquireLock must only be called within a transaction")
}

func (q *FakeQuerier) UpdateAPIKeyByID(_ context.Context, arg database.UpdateAPIKeyByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, apiKey := range q.apiKeys {
		if apiKey.ID != arg.ID {
			continue
		}
		apiKey.LastUsed = arg.LastUsed
		apiKey.ExpiresAt = arg.ExpiresAt
		apiKey.IPAddress = arg.IPAddress
		q.apiKeys[index] = apiKey
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateGitAuthLink(_ context.Context, arg database.UpdateGitAuthLinkParams) (database.GitAuthLink, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.GitAuthLink{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()
	for index, gitAuthLink := range q.gitAuthLinks {
		if gitAuthLink.ProviderID != arg.ProviderID {
			continue
		}
		if gitAuthLink.UserID != arg.UserID {
			continue
		}
		gitAuthLink.UpdatedAt = arg.UpdatedAt
		gitAuthLink.OAuthAccessToken = arg.OAuthAccessToken
		gitAuthLink.OAuthRefreshToken = arg.OAuthRefreshToken
		gitAuthLink.OAuthExpiry = arg.OAuthExpiry
		q.gitAuthLinks[index] = gitAuthLink

		return gitAuthLink, nil
	}
	return database.GitAuthLink{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateGitSSHKey(_ context.Context, arg database.UpdateGitSSHKeyParams) (database.GitSSHKey, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.GitSSHKey{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, key := range q.gitSSHKey {
		if key.UserID != arg.UserID {
			continue
		}
		key.UpdatedAt = arg.UpdatedAt
		key.PrivateKey = arg.PrivateKey
		key.PublicKey = arg.PublicKey
		q.gitSSHKey[index] = key
		return key, nil
	}
	return database.GitSSHKey{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateGroupByID(_ context.Context, arg database.UpdateGroupByIDParams) (database.Group, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Group{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, group := range q.groups {
		if group.ID == arg.ID {
			group.DisplayName = arg.DisplayName
			group.Name = arg.Name
			group.AvatarURL = arg.AvatarURL
			group.QuotaAllowance = arg.QuotaAllowance
			q.groups[i] = group
			return group, nil
		}
	}
	return database.Group{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateInactiveUsersToDormant(_ context.Context, params database.UpdateInactiveUsersToDormantParams) ([]database.UpdateInactiveUsersToDormantRow, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	var updated []database.UpdateInactiveUsersToDormantRow
	for index, user := range q.users {
		if user.Status == database.UserStatusActive && user.LastSeenAt.Before(params.LastSeenAfter) {
			q.users[index].Status = database.UserStatusDormant
			q.users[index].UpdatedAt = params.UpdatedAt
			updated = append(updated, database.UpdateInactiveUsersToDormantRow{
				ID:         user.ID,
				Email:      user.Email,
				LastSeenAt: user.LastSeenAt,
			})
		}
	}

	if len(updated) == 0 {
		return nil, sql.ErrNoRows
	}
	return updated, nil
}

func (q *FakeQuerier) UpdateMemberRoles(_ context.Context, arg database.UpdateMemberRolesParams) (database.OrganizationMember, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.OrganizationMember{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, mem := range q.organizationMembers {
		if mem.UserID == arg.UserID && mem.OrganizationID == arg.OrgID {
			uniqueRoles := make([]string, 0, len(arg.GrantedRoles))
			exist := make(map[string]struct{})
			for _, r := range arg.GrantedRoles {
				if _, ok := exist[r]; ok {
					continue
				}
				exist[r] = struct{}{}
				uniqueRoles = append(uniqueRoles, r)
			}
			sort.Strings(uniqueRoles)

			mem.Roles = uniqueRoles
			q.organizationMembers[i] = mem
			return mem, nil
		}
	}

	return database.OrganizationMember{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateProvisionerJobByID(_ context.Context, arg database.UpdateProvisionerJobByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, job := range q.provisionerJobs {
		if arg.ID != job.ID {
			continue
		}
		job.UpdatedAt = arg.UpdatedAt
		q.provisionerJobs[index] = job
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateProvisionerJobWithCancelByID(_ context.Context, arg database.UpdateProvisionerJobWithCancelByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, job := range q.provisionerJobs {
		if arg.ID != job.ID {
			continue
		}
		job.CanceledAt = arg.CanceledAt
		job.CompletedAt = arg.CompletedAt
		q.provisionerJobs[index] = job
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateProvisionerJobWithCompleteByID(_ context.Context, arg database.UpdateProvisionerJobWithCompleteByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, job := range q.provisionerJobs {
		if arg.ID != job.ID {
			continue
		}
		job.UpdatedAt = arg.UpdatedAt
		job.CompletedAt = arg.CompletedAt
		job.Error = arg.Error
		job.ErrorCode = arg.ErrorCode
		q.provisionerJobs[index] = job
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateReplica(_ context.Context, arg database.UpdateReplicaParams) (database.Replica, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Replica{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, replica := range q.replicas {
		if replica.ID != arg.ID {
			continue
		}
		replica.Hostname = arg.Hostname
		replica.StartedAt = arg.StartedAt
		replica.StoppedAt = arg.StoppedAt
		replica.UpdatedAt = arg.UpdatedAt
		replica.RelayAddress = arg.RelayAddress
		replica.RegionID = arg.RegionID
		replica.Version = arg.Version
		replica.Error = arg.Error
		replica.DatabaseLatency = arg.DatabaseLatency
		replica.Primary = arg.Primary
		q.replicas[index] = replica
		return replica, nil
	}
	return database.Replica{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateACLByID(_ context.Context, arg database.UpdateTemplateACLByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, template := range q.templates {
		if template.ID == arg.ID {
			template.GroupACL = arg.GroupACL
			template.UserACL = arg.UserACL

			q.templates[i] = template
			return nil
		}
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateActiveVersionByID(_ context.Context, arg database.UpdateTemplateActiveVersionByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, template := range q.templates {
		if template.ID != arg.ID {
			continue
		}
		template.ActiveVersionID = arg.ActiveVersionID
		template.UpdatedAt = arg.UpdatedAt
		q.templates[index] = template
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateDeletedByID(_ context.Context, arg database.UpdateTemplateDeletedByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, template := range q.templates {
		if template.ID != arg.ID {
			continue
		}
		template.Deleted = arg.Deleted
		template.UpdatedAt = arg.UpdatedAt
		q.templates[index] = template
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateMetaByID(_ context.Context, arg database.UpdateTemplateMetaByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for idx, tpl := range q.templates {
		if tpl.ID != arg.ID {
			continue
		}
		tpl.UpdatedAt = database.Now()
		tpl.Name = arg.Name
		tpl.DisplayName = arg.DisplayName
		tpl.Description = arg.Description
		tpl.Icon = arg.Icon
		q.templates[idx] = tpl
		return nil
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateScheduleByID(_ context.Context, arg database.UpdateTemplateScheduleByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for idx, tpl := range q.templates {
		if tpl.ID != arg.ID {
			continue
		}
		tpl.AllowUserAutostart = arg.AllowUserAutostart
		tpl.AllowUserAutostop = arg.AllowUserAutostop
		tpl.UpdatedAt = database.Now()
		tpl.DefaultTTL = arg.DefaultTTL
		tpl.MaxTTL = arg.MaxTTL
		tpl.RestartRequirementDaysOfWeek = arg.RestartRequirementDaysOfWeek
		tpl.RestartRequirementWeeks = arg.RestartRequirementWeeks
		tpl.FailureTTL = arg.FailureTTL
		tpl.InactivityTTL = arg.InactivityTTL
		tpl.LockedTTL = arg.LockedTTL
		q.templates[idx] = tpl
		return nil
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateVersionByID(_ context.Context, arg database.UpdateTemplateVersionByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, templateVersion := range q.templateVersions {
		if templateVersion.ID != arg.ID {
			continue
		}
		templateVersion.TemplateID = arg.TemplateID
		templateVersion.UpdatedAt = arg.UpdatedAt
		templateVersion.Name = arg.Name
		templateVersion.Message = arg.Message
		q.templateVersions[index] = templateVersion
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateVersionDescriptionByJobID(_ context.Context, arg database.UpdateTemplateVersionDescriptionByJobIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, templateVersion := range q.templateVersions {
		if templateVersion.JobID != arg.JobID {
			continue
		}
		templateVersion.Readme = arg.Readme
		templateVersion.UpdatedAt = arg.UpdatedAt
		q.templateVersions[index] = templateVersion
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateTemplateVersionGitAuthProvidersByJobID(_ context.Context, arg database.UpdateTemplateVersionGitAuthProvidersByJobIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, templateVersion := range q.templateVersions {
		if templateVersion.JobID != arg.JobID {
			continue
		}
		templateVersion.GitAuthProviders = arg.GitAuthProviders
		templateVersion.UpdatedAt = arg.UpdatedAt
		q.templateVersions[index] = templateVersion
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserDeletedByID(_ context.Context, params database.UpdateUserDeletedByIDParams) error {
	if err := validateDatabaseType(params); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, u := range q.users {
		if u.ID == params.ID {
			u.Deleted = params.Deleted
			q.users[i] = u
			// NOTE: In the real world, this is done by a trigger.
			i := 0
			for {
				if i >= len(q.apiKeys) {
					break
				}
				k := q.apiKeys[i]
				if k.UserID == u.ID {
					q.apiKeys[i] = q.apiKeys[len(q.apiKeys)-1]
					q.apiKeys = q.apiKeys[:len(q.apiKeys)-1]
					// We removed an element, so decrement
					i--
				}
				i++
			}
			return nil
		}
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserHashedPassword(_ context.Context, arg database.UpdateUserHashedPasswordParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, user := range q.users {
		if user.ID != arg.ID {
			continue
		}
		user.HashedPassword = arg.HashedPassword
		q.users[i] = user
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserLastSeenAt(_ context.Context, arg database.UpdateUserLastSeenAtParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, user := range q.users {
		if user.ID != arg.ID {
			continue
		}
		user.LastSeenAt = arg.LastSeenAt
		user.UpdatedAt = arg.UpdatedAt
		q.users[index] = user
		return user, nil
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserLink(_ context.Context, params database.UpdateUserLinkParams) (database.UserLink, error) {
	if err := validateDatabaseType(params); err != nil {
		return database.UserLink{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, link := range q.userLinks {
		if link.UserID == params.UserID && link.LoginType == params.LoginType {
			link.OAuthAccessToken = params.OAuthAccessToken
			link.OAuthRefreshToken = params.OAuthRefreshToken
			link.OAuthExpiry = params.OAuthExpiry

			q.userLinks[i] = link
			return link, nil
		}
	}

	return database.UserLink{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserLinkedID(_ context.Context, params database.UpdateUserLinkedIDParams) (database.UserLink, error) {
	if err := validateDatabaseType(params); err != nil {
		return database.UserLink{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, link := range q.userLinks {
		if link.UserID == params.UserID && link.LoginType == params.LoginType {
			link.LinkedID = params.LinkedID

			q.userLinks[i] = link
			return link, nil
		}
	}

	return database.UserLink{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserLoginType(_ context.Context, arg database.UpdateUserLoginTypeParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, u := range q.users {
		if u.ID == arg.UserID {
			u.LoginType = arg.NewLoginType
			if arg.NewLoginType != database.LoginTypePassword {
				u.HashedPassword = []byte{}
			}
			q.users[i] = u
			return u, nil
		}
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserProfile(_ context.Context, arg database.UpdateUserProfileParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, user := range q.users {
		if user.ID != arg.ID {
			continue
		}
		user.Email = arg.Email
		user.Username = arg.Username
		user.AvatarURL = arg.AvatarURL
		q.users[index] = user
		return user, nil
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserQuietHoursSchedule(_ context.Context, arg database.UpdateUserQuietHoursScheduleParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, user := range q.users {
		if user.ID != arg.ID {
			continue
		}
		user.QuietHoursSchedule = arg.QuietHoursSchedule
		q.users[index] = user
		return user, nil
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserRoles(_ context.Context, arg database.UpdateUserRolesParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, user := range q.users {
		if user.ID != arg.ID {
			continue
		}

		// Set new roles
		user.RBACRoles = arg.GrantedRoles
		// Remove duplicates and sort
		uniqueRoles := make([]string, 0, len(user.RBACRoles))
		exist := make(map[string]struct{})
		for _, r := range user.RBACRoles {
			if _, ok := exist[r]; ok {
				continue
			}
			exist[r] = struct{}{}
			uniqueRoles = append(uniqueRoles, r)
		}
		sort.Strings(uniqueRoles)
		user.RBACRoles = uniqueRoles

		q.users[index] = user
		return user, nil
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateUserStatus(_ context.Context, arg database.UpdateUserStatusParams) (database.User, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.User{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, user := range q.users {
		if user.ID != arg.ID {
			continue
		}
		user.Status = arg.Status
		user.UpdatedAt = arg.UpdatedAt
		q.users[index] = user
		return user, nil
	}
	return database.User{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspace(_ context.Context, arg database.UpdateWorkspaceParams) (database.Workspace, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Workspace{}, err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, workspace := range q.workspaces {
		if workspace.Deleted || workspace.ID != arg.ID {
			continue
		}
		for _, other := range q.workspaces {
			if other.Deleted || other.ID == workspace.ID || workspace.OwnerID != other.OwnerID {
				continue
			}
			if other.Name == arg.Name {
				return database.Workspace{}, errDuplicateKey
			}
		}

		workspace.Name = arg.Name
		q.workspaces[i] = workspace

		return workspace, nil
	}

	return database.Workspace{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceAgentConnectionByID(_ context.Context, arg database.UpdateWorkspaceAgentConnectionByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, agent := range q.workspaceAgents {
		if agent.ID != arg.ID {
			continue
		}
		agent.FirstConnectedAt = arg.FirstConnectedAt
		agent.LastConnectedAt = arg.LastConnectedAt
		agent.DisconnectedAt = arg.DisconnectedAt
		agent.UpdatedAt = arg.UpdatedAt
		q.workspaceAgents[index] = agent
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceAgentLifecycleStateByID(_ context.Context, arg database.UpdateWorkspaceAgentLifecycleStateByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()
	for i, agent := range q.workspaceAgents {
		if agent.ID == arg.ID {
			agent.LifecycleState = arg.LifecycleState
			agent.StartedAt = arg.StartedAt
			agent.ReadyAt = arg.ReadyAt
			q.workspaceAgents[i] = agent
			return nil
		}
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceAgentLogOverflowByID(_ context.Context, arg database.UpdateWorkspaceAgentLogOverflowByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()
	for i, agent := range q.workspaceAgents {
		if agent.ID == arg.ID {
			agent.LogsOverflowed = arg.LogsOverflowed
			q.workspaceAgents[i] = agent
			return nil
		}
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceAgentMetadata(_ context.Context, arg database.UpdateWorkspaceAgentMetadataParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	//nolint:gosimple
	updated := database.WorkspaceAgentMetadatum{
		WorkspaceAgentID: arg.WorkspaceAgentID,
		Key:              arg.Key,
		Value:            arg.Value,
		Error:            arg.Error,
		CollectedAt:      arg.CollectedAt,
	}

	for i, m := range q.workspaceAgentMetadata {
		if m.WorkspaceAgentID == arg.WorkspaceAgentID && m.Key == arg.Key {
			q.workspaceAgentMetadata[i] = updated
			return nil
		}
	}

	return nil
}

func (q *FakeQuerier) UpdateWorkspaceAgentStartupByID(_ context.Context, arg database.UpdateWorkspaceAgentStartupByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, agent := range q.workspaceAgents {
		if agent.ID != arg.ID {
			continue
		}

		agent.Version = arg.Version
		agent.ExpandedDirectory = arg.ExpandedDirectory
		agent.Subsystem = arg.Subsystem
		q.workspaceAgents[index] = agent
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceAppHealthByID(_ context.Context, arg database.UpdateWorkspaceAppHealthByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, app := range q.workspaceApps {
		if app.ID != arg.ID {
			continue
		}
		app.Health = arg.Health
		q.workspaceApps[index] = app
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceAutostart(_ context.Context, arg database.UpdateWorkspaceAutostartParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, workspace := range q.workspaces {
		if workspace.ID != arg.ID {
			continue
		}
		workspace.AutostartSchedule = arg.AutostartSchedule
		q.workspaces[index] = workspace
		return nil
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceBuildByID(_ context.Context, arg database.UpdateWorkspaceBuildByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, workspaceBuild := range q.workspaceBuilds {
		if workspaceBuild.ID != arg.ID {
			continue
		}
		workspaceBuild.UpdatedAt = arg.UpdatedAt
		workspaceBuild.ProvisionerState = arg.ProvisionerState
		workspaceBuild.Deadline = arg.Deadline
		workspaceBuild.MaxDeadline = arg.MaxDeadline
		q.workspaceBuilds[index] = workspaceBuild
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceBuildCostByID(_ context.Context, arg database.UpdateWorkspaceBuildCostByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, workspaceBuild := range q.workspaceBuilds {
		if workspaceBuild.ID != arg.ID {
			continue
		}
		workspaceBuild.DailyCost = arg.DailyCost
		q.workspaceBuilds[index] = workspaceBuild
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceDeletedByID(_ context.Context, arg database.UpdateWorkspaceDeletedByIDParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, workspace := range q.workspaces {
		if workspace.ID != arg.ID {
			continue
		}
		workspace.Deleted = arg.Deleted
		q.workspaces[index] = workspace
		return nil
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceLastUsedAt(_ context.Context, arg database.UpdateWorkspaceLastUsedAtParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, workspace := range q.workspaces {
		if workspace.ID != arg.ID {
			continue
		}
		workspace.LastUsedAt = arg.LastUsedAt
		q.workspaces[index] = workspace
		return nil
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceLockedDeletingAt(_ context.Context, arg database.UpdateWorkspaceLockedDeletingAtParams) (database.Workspace, error) {
	if err := validateDatabaseType(arg); err != nil {
		return database.Workspace{}, err
	}
	q.mutex.Lock()
	defer q.mutex.Unlock()
	for index, workspace := range q.workspaces {
		if workspace.ID != arg.ID {
			continue
		}
		workspace.LockedAt = arg.LockedAt
		if workspace.LockedAt.Time.IsZero() {
			workspace.LastUsedAt = database.Now()
			workspace.DeletingAt = sql.NullTime{}
		}
		if !workspace.LockedAt.Time.IsZero() {
			var template database.TemplateTable
			for _, t := range q.templates {
				if t.ID == workspace.TemplateID {
					template = t
					break
				}
			}
			if template.ID == uuid.Nil {
				return database.Workspace{}, xerrors.Errorf("unable to find workspace template")
			}
			if template.LockedTTL > 0 {
				workspace.DeletingAt = sql.NullTime{
					Valid: true,
					Time:  workspace.LockedAt.Time.Add(time.Duration(template.LockedTTL)),
				}
			}
		}
		q.workspaces[index] = workspace
		return workspace, nil
	}
	return database.Workspace{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceProxy(_ context.Context, arg database.UpdateWorkspaceProxyParams) (database.WorkspaceProxy, error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for _, p := range q.workspaceProxies {
		if p.Name == arg.Name && p.ID != arg.ID {
			return database.WorkspaceProxy{}, errDuplicateKey
		}
	}

	for i, p := range q.workspaceProxies {
		if p.ID == arg.ID {
			p.Name = arg.Name
			p.DisplayName = arg.DisplayName
			p.Icon = arg.Icon
			if len(p.TokenHashedSecret) > 0 {
				p.TokenHashedSecret = arg.TokenHashedSecret
			}
			q.workspaceProxies[i] = p
			return p, nil
		}
	}
	return database.WorkspaceProxy{}, sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceProxyDeleted(_ context.Context, arg database.UpdateWorkspaceProxyDeletedParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for i, p := range q.workspaceProxies {
		if p.ID == arg.ID {
			p.Deleted = arg.Deleted
			p.UpdatedAt = database.Now()
			q.workspaceProxies[i] = p
			return nil
		}
	}
	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspaceTTL(_ context.Context, arg database.UpdateWorkspaceTTLParams) error {
	if err := validateDatabaseType(arg); err != nil {
		return err
	}

	q.mutex.Lock()
	defer q.mutex.Unlock()

	for index, workspace := range q.workspaces {
		if workspace.ID != arg.ID {
			continue
		}
		workspace.Ttl = arg.Ttl
		q.workspaces[index] = workspace
		return nil
	}

	return sql.ErrNoRows
}

func (q *FakeQuerier) UpdateWorkspacesDeletingAtByTemplateID(_ context.Context, arg database.UpdateWorkspacesDeletingAtByTemplateIDParams) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	err := validateDatabaseType(arg)
	if err != nil {
		return err
	}

	for i, ws := range q.workspaces {
		if ws.LockedAt.Time.IsZero() {
			continue
		}
		deletingAt := sql.NullTime{
			Valid: arg.LockedTtlMs > 0,
		}
		if arg.LockedTtlMs > 0 {
			deletingAt.Time = ws.LockedAt.Time.Add(time.Duration(arg.LockedTtlMs) * time.Millisecond)
		}
		ws.DeletingAt = deletingAt
		q.workspaces[i] = ws
	}

	return nil
}

func (q *FakeQuerier) UpsertAppSecurityKey(_ context.Context, data string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.appSecurityKey = data
	return nil
}

func (q *FakeQuerier) UpsertDefaultProxy(_ context.Context, arg database.UpsertDefaultProxyParams) error {
	q.defaultProxyDisplayName = arg.DisplayName
	q.defaultProxyIconURL = arg.IconUrl
	return nil
}

func (q *FakeQuerier) UpsertLastUpdateCheck(_ context.Context, data string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.lastUpdateCheck = []byte(data)
	return nil
}

func (q *FakeQuerier) UpsertLogoURL(_ context.Context, data string) error {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	q.logoURL = data
	return nil
}

func (q *FakeQuerier) UpsertOAuthSigningKey(_ context.Context, value string) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.oauthSigningKey = value
	return nil
}

func (q *FakeQuerier) UpsertServiceBanner(_ context.Context, data string) error {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	q.serviceBanner = []byte(data)
	return nil
}

func (*FakeQuerier) UpsertTailnetAgent(context.Context, database.UpsertTailnetAgentParams) (database.TailnetAgent, error) {
	return database.TailnetAgent{}, ErrUnimplemented
}

func (*FakeQuerier) UpsertTailnetClient(context.Context, database.UpsertTailnetClientParams) (database.TailnetClient, error) {
	return database.TailnetClient{}, ErrUnimplemented
}

func (*FakeQuerier) UpsertTailnetCoordinator(context.Context, uuid.UUID) (database.TailnetCoordinator, error) {
	return database.TailnetCoordinator{}, ErrUnimplemented
}

func (q *FakeQuerier) GetAuthorizedTemplates(ctx context.Context, arg database.GetTemplatesWithFilterParams, prepared rbac.PreparedAuthorized) ([]database.Template, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// Call this to match the same function calls as the SQL implementation.
	if prepared != nil {
		_, err := prepared.CompileToSQL(ctx, rbac.ConfigWithACL())
		if err != nil {
			return nil, err
		}
	}

	var templates []database.Template
	for _, templateTable := range q.templates {
		template := q.templateWithUserNoLock(templateTable)
		if prepared != nil && prepared.Authorize(ctx, template.RBACObject()) != nil {
			continue
		}

		if template.Deleted != arg.Deleted {
			continue
		}
		if arg.OrganizationID != uuid.Nil && template.OrganizationID != arg.OrganizationID {
			continue
		}

		if arg.ExactName != "" && !strings.EqualFold(template.Name, arg.ExactName) {
			continue
		}

		if len(arg.IDs) > 0 {
			match := false
			for _, id := range arg.IDs {
				if template.ID == id {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		templates = append(templates, template)
	}
	if len(templates) > 0 {
		slices.SortFunc(templates, func(i, j database.Template) bool {
			if i.Name != j.Name {
				return i.Name < j.Name
			}
			return i.ID.String() < j.ID.String()
		})
		return templates, nil
	}

	return nil, sql.ErrNoRows
}

func (q *FakeQuerier) GetTemplateGroupRoles(_ context.Context, id uuid.UUID) ([]database.TemplateGroup, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var template database.TemplateTable
	for _, t := range q.templates {
		if t.ID == id {
			template = t
			break
		}
	}

	if template.ID == uuid.Nil {
		return nil, sql.ErrNoRows
	}

	groups := make([]database.TemplateGroup, 0, len(template.GroupACL))
	for k, v := range template.GroupACL {
		group, err := q.getGroupByIDNoLock(context.Background(), uuid.MustParse(k))
		if err != nil && !xerrors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("get group by ID: %w", err)
		}
		// We don't delete groups from the map if they
		// get deleted so just skip.
		if xerrors.Is(err, sql.ErrNoRows) {
			continue
		}

		groups = append(groups, database.TemplateGroup{
			Group:   group,
			Actions: v,
		})
	}

	return groups, nil
}

func (q *FakeQuerier) GetTemplateUserRoles(_ context.Context, id uuid.UUID) ([]database.TemplateUser, error) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var template database.TemplateTable
	for _, t := range q.templates {
		if t.ID == id {
			template = t
			break
		}
	}

	if template.ID == uuid.Nil {
		return nil, sql.ErrNoRows
	}

	users := make([]database.TemplateUser, 0, len(template.UserACL))
	for k, v := range template.UserACL {
		user, err := q.getUserByIDNoLock(uuid.MustParse(k))
		if err != nil && xerrors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("get user by ID: %w", err)
		}
		// We don't delete users from the map if they
		// get deleted so just skip.
		if xerrors.Is(err, sql.ErrNoRows) {
			continue
		}

		if user.Deleted || user.Status == database.UserStatusSuspended {
			continue
		}

		users = append(users, database.TemplateUser{
			User:    user,
			Actions: v,
		})
	}

	return users, nil
}

//nolint:gocyclo
func (q *FakeQuerier) GetAuthorizedWorkspaces(ctx context.Context, arg database.GetWorkspacesParams, prepared rbac.PreparedAuthorized) ([]database.GetWorkspacesRow, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if prepared != nil {
		// Call this to match the same function calls as the SQL implementation.
		_, err := prepared.CompileToSQL(ctx, rbac.ConfigWithoutACL())
		if err != nil {
			return nil, err
		}
	}

	workspaces := make([]database.Workspace, 0)
	for _, workspace := range q.workspaces {
		if arg.OwnerID != uuid.Nil && workspace.OwnerID != arg.OwnerID {
			continue
		}

		if arg.OwnerUsername != "" {
			owner, err := q.getUserByIDNoLock(workspace.OwnerID)
			if err == nil && !strings.EqualFold(arg.OwnerUsername, owner.Username) {
				continue
			}
		}

		if arg.TemplateName != "" {
			template, err := q.getTemplateByIDNoLock(ctx, workspace.TemplateID)
			if err == nil && !strings.EqualFold(arg.TemplateName, template.Name) {
				continue
			}
		}

		if !arg.Deleted && workspace.Deleted {
			continue
		}

		if arg.Name != "" && !strings.Contains(strings.ToLower(workspace.Name), strings.ToLower(arg.Name)) {
			continue
		}

		if arg.Status != "" {
			build, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, workspace.ID)
			if err != nil {
				return nil, xerrors.Errorf("get latest build: %w", err)
			}

			job, err := q.getProvisionerJobByIDNoLock(ctx, build.JobID)
			if err != nil {
				return nil, xerrors.Errorf("get provisioner job: %w", err)
			}

			// This logic should match the logic in the workspace.sql file.
			var statusMatch bool
			switch database.WorkspaceStatus(arg.Status) {
			case database.WorkspaceStatusPending:
				statusMatch = isNull(job.StartedAt)
			case database.WorkspaceStatusStarting:
				statusMatch = isNotNull(job.StartedAt) &&
					isNull(job.CanceledAt) &&
					isNull(job.CompletedAt) &&
					time.Since(job.UpdatedAt) < 30*time.Second &&
					build.Transition == database.WorkspaceTransitionStart

			case database.WorkspaceStatusRunning:
				statusMatch = isNotNull(job.CompletedAt) &&
					isNull(job.CanceledAt) &&
					isNull(job.Error) &&
					build.Transition == database.WorkspaceTransitionStart

			case database.WorkspaceStatusStopping:
				statusMatch = isNotNull(job.StartedAt) &&
					isNull(job.CanceledAt) &&
					isNull(job.CompletedAt) &&
					time.Since(job.UpdatedAt) < 30*time.Second &&
					build.Transition == database.WorkspaceTransitionStop

			case database.WorkspaceStatusStopped:
				statusMatch = isNotNull(job.CompletedAt) &&
					isNull(job.CanceledAt) &&
					isNull(job.Error) &&
					build.Transition == database.WorkspaceTransitionStop
			case database.WorkspaceStatusFailed:
				statusMatch = (isNotNull(job.CanceledAt) && isNotNull(job.Error)) ||
					(isNotNull(job.CompletedAt) && isNotNull(job.Error))

			case database.WorkspaceStatusCanceling:
				statusMatch = isNotNull(job.CanceledAt) &&
					isNull(job.CompletedAt)

			case database.WorkspaceStatusCanceled:
				statusMatch = isNotNull(job.CanceledAt) &&
					isNotNull(job.CompletedAt)

			case database.WorkspaceStatusDeleted:
				statusMatch = isNotNull(job.StartedAt) &&
					isNull(job.CanceledAt) &&
					isNotNull(job.CompletedAt) &&
					time.Since(job.UpdatedAt) < 30*time.Second &&
					build.Transition == database.WorkspaceTransitionDelete &&
					isNull(job.Error)

			case database.WorkspaceStatusDeleting:
				statusMatch = isNull(job.CompletedAt) &&
					isNull(job.CanceledAt) &&
					isNull(job.Error) &&
					build.Transition == database.WorkspaceTransitionDelete

			default:
				return nil, xerrors.Errorf("unknown workspace status in filter: %q", arg.Status)
			}
			if !statusMatch {
				continue
			}
		}

		if arg.HasAgent != "" {
			build, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, workspace.ID)
			if err != nil {
				return nil, xerrors.Errorf("get latest build: %w", err)
			}

			job, err := q.getProvisionerJobByIDNoLock(ctx, build.JobID)
			if err != nil {
				return nil, xerrors.Errorf("get provisioner job: %w", err)
			}

			workspaceResources, err := q.getWorkspaceResourcesByJobIDNoLock(ctx, job.ID)
			if err != nil {
				return nil, xerrors.Errorf("get workspace resources: %w", err)
			}

			var workspaceResourceIDs []uuid.UUID
			for _, wr := range workspaceResources {
				workspaceResourceIDs = append(workspaceResourceIDs, wr.ID)
			}

			workspaceAgents, err := q.getWorkspaceAgentsByResourceIDsNoLock(ctx, workspaceResourceIDs)
			if err != nil {
				return nil, xerrors.Errorf("get workspace agents: %w", err)
			}

			var hasAgentMatched bool
			for _, wa := range workspaceAgents {
				if mapAgentStatus(wa, arg.AgentInactiveDisconnectTimeoutSeconds) == arg.HasAgent {
					hasAgentMatched = true
				}
			}

			if !hasAgentMatched {
				continue
			}
		}

		// We omit locked workspaces by default.
		if arg.LockedAt.IsZero() && workspace.LockedAt.Valid {
			continue
		}

		// Filter out workspaces that are locked after the timestamp.
		if !arg.LockedAt.IsZero() && workspace.LockedAt.Time.Before(arg.LockedAt) {
			continue
		}

		if len(arg.TemplateIDs) > 0 {
			match := false
			for _, id := range arg.TemplateIDs {
				if workspace.TemplateID == id {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		// If the filter exists, ensure the object is authorized.
		if prepared != nil && prepared.Authorize(ctx, workspace.RBACObject()) != nil {
			continue
		}
		workspaces = append(workspaces, workspace)
	}

	// Sort workspaces (ORDER BY)
	isRunning := func(build database.WorkspaceBuild, job database.ProvisionerJob) bool {
		return job.CompletedAt.Valid && !job.CanceledAt.Valid && !job.Error.Valid && build.Transition == database.WorkspaceTransitionStart
	}

	preloadedWorkspaceBuilds := map[uuid.UUID]database.WorkspaceBuild{}
	preloadedProvisionerJobs := map[uuid.UUID]database.ProvisionerJob{}
	preloadedUsers := map[uuid.UUID]database.User{}

	for _, w := range workspaces {
		build, err := q.getLatestWorkspaceBuildByWorkspaceIDNoLock(ctx, w.ID)
		if err == nil {
			preloadedWorkspaceBuilds[w.ID] = build
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("get latest build: %w", err)
		}

		job, err := q.getProvisionerJobByIDNoLock(ctx, build.JobID)
		if err == nil {
			preloadedProvisionerJobs[w.ID] = job
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("get provisioner job: %w", err)
		}

		user, err := q.getUserByIDNoLock(w.OwnerID)
		if err == nil {
			preloadedUsers[w.ID] = user
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("get user: %w", err)
		}
	}

	sort.Slice(workspaces, func(i, j int) bool {
		w1 := workspaces[i]
		w2 := workspaces[j]

		// Order by: running first
		w1IsRunning := isRunning(preloadedWorkspaceBuilds[w1.ID], preloadedProvisionerJobs[w1.ID])
		w2IsRunning := isRunning(preloadedWorkspaceBuilds[w2.ID], preloadedProvisionerJobs[w2.ID])

		if w1IsRunning && !w2IsRunning {
			return true
		}

		if !w1IsRunning && w2IsRunning {
			return false
		}

		// Order by: usernames
		if w1.ID != w2.ID {
			return sort.StringsAreSorted([]string{preloadedUsers[w1.ID].Username, preloadedUsers[w2.ID].Username})
		}

		// Order by: workspace names
		return sort.StringsAreSorted([]string{w1.Name, w2.Name})
	})

	beforePageCount := len(workspaces)

	if arg.Offset > 0 {
		if int(arg.Offset) > len(workspaces) {
			return []database.GetWorkspacesRow{}, nil
		}
		workspaces = workspaces[arg.Offset:]
	}
	if arg.Limit > 0 {
		if int(arg.Limit) > len(workspaces) {
			return q.convertToWorkspaceRowsNoLock(ctx, workspaces, int64(beforePageCount)), nil
		}
		workspaces = workspaces[:arg.Limit]
	}

	return q.convertToWorkspaceRowsNoLock(ctx, workspaces, int64(beforePageCount)), nil
}

func (q *FakeQuerier) GetAuthorizedUsers(ctx context.Context, arg database.GetUsersParams, prepared rbac.PreparedAuthorized) ([]database.GetUsersRow, error) {
	if err := validateDatabaseType(arg); err != nil {
		return nil, err
	}

	// Call this to match the same function calls as the SQL implementation.
	if prepared != nil {
		_, err := prepared.CompileToSQL(ctx, regosql.ConvertConfig{
			VariableConverter: regosql.UserConverter(),
		})
		if err != nil {
			return nil, err
		}
	}

	users, err := q.GetUsers(ctx, arg)
	if err != nil {
		return nil, err
	}

	q.mutex.RLock()
	defer q.mutex.RUnlock()

	filteredUsers := make([]database.GetUsersRow, 0, len(users))
	for _, user := range users {
		// If the filter exists, ensure the object is authorized.
		if prepared != nil && prepared.Authorize(ctx, user.RBACObject()) != nil {
			continue
		}

		filteredUsers = append(filteredUsers, user)
	}
	return filteredUsers, nil
}

package coderd

import (
	"net/http"

	"github.com/coder/coder/coderd/httpapi"
)

// @Summary Removed: Get parameters by template version
// @ID removed-get-parameters-by-template-version
// @Security CoderSessionToken
// @Tags Templates
// @Param templateversion path string true "Template version ID" format(uuid)
// @Success 200
// @Router /templateversions/{templateversion}/parameters [get]
func templateVersionParametersDeprecated(rw http.ResponseWriter, r *http.Request) {
	httpapi.Write(r.Context(), rw, http.StatusOK, []struct{}{})
}

// @Summary Removed: Get schema by template version
// @ID removed-get-schema-by-template-version
// @Security CoderSessionToken
// @Tags Templates
// @Param templateversion path string true "Template version ID" format(uuid)
// @Success 200
// @Router /templateversions/{templateversion}/schema [get]
func templateVersionSchemaDeprecated(rw http.ResponseWriter, r *http.Request) {
	httpapi.Write(r.Context(), rw, http.StatusOK, []struct{}{})
}

// @Summary Removed: Patch workspace agent logs
// @ID removed-patch-workspace-agent-logs
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Agents
// @Param request body agentsdk.PatchLogs true "logs"
// @Success 200 {object} codersdk.Response
// @Router /workspaceagents/me/startup-logs [patch]
func (api *API) patchWorkspaceAgentLogsDeprecated(rw http.ResponseWriter, r *http.Request) {
	api.patchWorkspaceAgentLogs(rw, r)
}

// @Summary Removed: Get logs by workspace agent
// @ID removed-get-logs-by-workspace-agent
// @Security CoderSessionToken
// @Produce json
// @Tags Agents
// @Param workspaceagent path string true "Workspace agent ID" format(uuid)
// @Param before query int false "Before log id"
// @Param after query int false "After log id"
// @Param follow query bool false "Follow log stream"
// @Param no_compression query bool false "Disable compression for WebSocket connection"
// @Success 200 {array} codersdk.WorkspaceAgentLog
// @Router /workspaceagents/{workspaceagent}/startup-logs [get]
func (api *API) workspaceAgentLogsDeprecated(rw http.ResponseWriter, r *http.Request) {
	api.workspaceAgentLogs(rw, r)
}

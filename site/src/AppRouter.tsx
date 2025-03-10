import { FullScreenLoader } from "components/Loader/FullScreenLoader"
import { TemplateLayout } from "components/TemplateLayout/TemplateLayout"
import { UsersLayout } from "components/UsersLayout/UsersLayout"
import IndexPage from "pages"
import AuditPage from "pages/AuditPage/AuditPage"
import GroupsPage from "pages/GroupsPage/GroupsPage"
import LoginPage from "pages/LoginPage/LoginPage"
import { SetupPage } from "pages/SetupPage/SetupPage"
import { TemplateSettingsPage } from "pages/TemplateSettingsPage/TemplateGeneralSettingsPage/TemplateSettingsPage"
import TemplatesPage from "pages/TemplatesPage/TemplatesPage"
import UsersPage from "pages/UsersPage/UsersPage"
import WorkspacesPage from "pages/WorkspacesPage/WorkspacesPage"
import { FC, lazy, Suspense } from "react"
import { Route, Routes, BrowserRouter as Router } from "react-router-dom"
import { DashboardLayout } from "./components/Dashboard/DashboardLayout"
import { RequireAuth } from "./components/RequireAuth/RequireAuth"
import { SettingsLayout } from "./components/SettingsLayout/SettingsLayout"
import { DeploySettingsLayout } from "components/DeploySettingsLayout/DeploySettingsLayout"
import { TemplateSettingsLayout } from "pages/TemplateSettingsPage/TemplateSettingsLayout"
import { WorkspaceSettingsLayout } from "pages/WorkspaceSettingsPage/WorkspaceSettingsLayout"

// Lazy load pages
// - Pages that are secondary, not in the main navigation or not usually accessed
// - Pages that use heavy dependencies like charts or time libraries
const NotFoundPage = lazy(() => import("./pages/404Page/404Page"))
const CliAuthenticationPage = lazy(
  () => import("./pages/CliAuthPage/CliAuthPage"),
)
const AccountPage = lazy(
  () => import("./pages/UserSettingsPage/AccountPage/AccountPage"),
)
const SecurityPage = lazy(
  () => import("./pages/UserSettingsPage/SecurityPage/SecurityPage"),
)
const SSHKeysPage = lazy(
  () => import("./pages/UserSettingsPage/SSHKeysPage/SSHKeysPage"),
)
const TokensPage = lazy(
  () => import("./pages/UserSettingsPage/TokensPage/TokensPage"),
)
const WorkspaceProxyPage = lazy(
  () =>
    import("./pages/UserSettingsPage/WorkspaceProxyPage/WorkspaceProxyPage"),
)
const CreateUserPage = lazy(
  () => import("./pages/UsersPage/CreateUserPage/CreateUserPage"),
)
const WorkspaceBuildPage = lazy(
  () => import("./pages/WorkspaceBuildPage/WorkspaceBuildPage"),
)
const WorkspacePage = lazy(() => import("./pages/WorkspacePage/WorkspacePage"))
const WorkspaceSchedulePage = lazy(
  () =>
    import(
      "./pages/WorkspaceSettingsPage/WorkspaceSchedulePage/WorkspaceSchedulePage"
    ),
)
const WorkspaceParametersPage = lazy(
  () =>
    import(
      "./pages/WorkspaceSettingsPage/WorkspaceParametersPage/WorkspaceParametersPage"
    ),
)
const TerminalPage = lazy(() => import("./pages/TerminalPage/TerminalPage"))
const TemplatePermissionsPage = lazy(
  () =>
    import(
      "./pages/TemplateSettingsPage/TemplatePermissionsPage/TemplatePermissionsPage"
    ),
)
const TemplateSummaryPage = lazy(
  () => import("./pages/TemplatePage/TemplateSummaryPage/TemplateSummaryPage"),
)
const CreateWorkspacePage = lazy(
  () => import("./pages/CreateWorkspacePage/CreateWorkspacePage"),
)
const CreateGroupPage = lazy(() => import("./pages/GroupsPage/CreateGroupPage"))
const GroupPage = lazy(() => import("./pages/GroupsPage/GroupPage"))
const SettingsGroupPage = lazy(
  () => import("./pages/GroupsPage/SettingsGroupPage"),
)
const GeneralSettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/GeneralSettingsPage/GeneralSettingsPage"
    ),
)
const SecuritySettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/SecuritySettingsPage/SecuritySettingsPage"
    ),
)
const AppearanceSettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/AppearanceSettingsPage/AppearanceSettingsPage"
    ),
)
const UserAuthSettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/UserAuthSettingsPage/UserAuthSettingsPage"
    ),
)
const GitAuthSettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/GitAuthSettingsPage/GitAuthSettingsPage"
    ),
)
const NetworkSettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/NetworkSettingsPage/NetworkSettingsPage"
    ),
)
const GitAuthPage = lazy(() => import("./pages/GitAuthPage/GitAuthPage"))
const TemplateVersionPage = lazy(
  () => import("./pages/TemplateVersionPage/TemplateVersionPage"),
)
const TemplateVersionEditorPage = lazy(
  () =>
    import(
      "./pages/TemplateVersionPage/TemplateVersionEditorPage/TemplateVersionEditorPage"
    ),
)
const StarterTemplatesPage = lazy(
  () => import("./pages/StarterTemplatesPage/StarterTemplatesPage"),
)
const StarterTemplatePage = lazy(
  () => import("pages/StarterTemplatePage/StarterTemplatePage"),
)
const CreateTemplatePage = lazy(
  () => import("./pages/CreateTemplatePage/CreateTemplatePage"),
)
const TemplateVariablesPage = lazy(
  () =>
    import(
      "./pages/TemplateSettingsPage/TemplateVariablesPage/TemplateVariablesPage"
    ),
)
const WorkspaceSettingsPage = lazy(
  () => import("./pages/WorkspaceSettingsPage/WorkspaceSettingsPage"),
)
const CreateTokenPage = lazy(
  () => import("./pages/CreateTokenPage/CreateTokenPage"),
)

const TemplateDocsPage = lazy(
  () => import("./pages/TemplatePage/TemplateDocsPage/TemplateDocsPage"),
)

const TemplateFilesPage = lazy(
  () => import("./pages/TemplatePage/TemplateFilesPage/TemplateFilesPage"),
)

const TemplateVersionsPage = lazy(
  () =>
    import("./pages/TemplatePage/TemplateVersionsPage/TemplateVersionsPage"),
)
const TemplateSchedulePage = lazy(
  () =>
    import(
      "./pages/TemplateSettingsPage/TemplateSchedulePage/TemplateSchedulePage"
    ),
)

const LicensesSettingsPage = lazy(
  () =>
    import(
      "./pages/DeploySettingsPage/LicensesSettingsPage/LicensesSettingsPage"
    ),
)
const AddNewLicensePage = lazy(
  () =>
    import("./pages/DeploySettingsPage/LicensesSettingsPage/AddNewLicensePage"),
)
const TemplateEmbedPage = lazy(
  () => import("./pages/TemplatePage/TemplateEmbedPage/TemplateEmbedPage"),
)
const TemplateInsightsPage = lazy(
  () =>
    import("./pages/TemplatePage/TemplateInsightsPage/TemplateInsightsPage"),
)
const HealthPage = lazy(() => import("./pages/HealthPage/HealthPage"))

export const AppRouter: FC = () => {
  return (
    <Suspense fallback={<FullScreenLoader />}>
      <Router>
        <Routes>
          <Route path="login" element={<LoginPage />} />
          <Route path="setup" element={<SetupPage />} />

          {/* Dashboard routes */}
          <Route element={<RequireAuth />}>
            <Route element={<DashboardLayout />}>
              <Route index element={<IndexPage />} />

              <Route path="health" element={<HealthPage />} />

              <Route path="gitauth/:provider" element={<GitAuthPage />} />

              <Route path="workspaces" element={<WorkspacesPage />} />

              <Route path="starter-templates">
                <Route index element={<StarterTemplatesPage />} />
                <Route path=":exampleId" element={<StarterTemplatePage />} />
              </Route>

              <Route path="templates">
                <Route index element={<TemplatesPage />} />
                <Route path="new" element={<CreateTemplatePage />} />
                <Route path=":template">
                  <Route element={<TemplateLayout />}>
                    <Route index element={<TemplateSummaryPage />} />
                    <Route path="docs" element={<TemplateDocsPage />} />
                    <Route path="files" element={<TemplateFilesPage />} />
                    <Route path="versions" element={<TemplateVersionsPage />} />
                    <Route path="embed" element={<TemplateEmbedPage />} />
                    <Route path="insights" element={<TemplateInsightsPage />} />
                  </Route>

                  <Route path="workspace" element={<CreateWorkspacePage />} />

                  <Route path="settings" element={<TemplateSettingsLayout />}>
                    <Route index element={<TemplateSettingsPage />} />
                    <Route
                      path="permissions"
                      element={<TemplatePermissionsPage />}
                    />
                    <Route
                      path="variables"
                      element={<TemplateVariablesPage />}
                    />
                    <Route path="schedule" element={<TemplateSchedulePage />} />
                  </Route>

                  <Route path="versions">
                    <Route path=":version">
                      <Route index element={<TemplateVersionPage />} />
                      <Route
                        path="edit"
                        element={<TemplateVersionEditorPage />}
                      />
                    </Route>
                  </Route>
                </Route>
              </Route>

              <Route path="users">
                <Route element={<UsersLayout />}>
                  <Route index element={<UsersPage />} />
                </Route>

                <Route path="create" element={<CreateUserPage />} />
              </Route>

              <Route path="/groups">
                <Route element={<UsersLayout />}>
                  <Route index element={<GroupsPage />} />
                </Route>

                <Route path="create" element={<CreateGroupPage />} />
                <Route path=":groupId" element={<GroupPage />} />
                <Route
                  path=":groupId/settings"
                  element={<SettingsGroupPage />}
                />
              </Route>

              <Route path="/audit" element={<AuditPage />} />

              <Route
                path="/settings/deployment"
                element={<DeploySettingsLayout />}
              >
                <Route path="general" element={<GeneralSettingsPage />} />
                <Route path="licenses" element={<LicensesSettingsPage />} />
                <Route path="licenses/add" element={<AddNewLicensePage />} />
                <Route path="security" element={<SecuritySettingsPage />} />
                <Route path="appearance" element={<AppearanceSettingsPage />} />
                <Route path="network" element={<NetworkSettingsPage />} />
                <Route path="userauth" element={<UserAuthSettingsPage />} />
                <Route path="gitauth" element={<GitAuthSettingsPage />} />
                <Route
                  path="workspace-proxies"
                  element={<WorkspaceProxyPage />}
                />
              </Route>

              <Route path="settings" element={<SettingsLayout />}>
                <Route path="account" element={<AccountPage />} />
                <Route path="security" element={<SecurityPage />} />
                <Route path="ssh-keys" element={<SSHKeysPage />} />
                <Route path="tokens">
                  <Route index element={<TokensPage />} />
                  <Route path="new" element={<CreateTokenPage />} />
                </Route>
              </Route>

              <Route path="/:username">
                <Route path=":workspace">
                  <Route index element={<WorkspacePage />} />
                  <Route
                    path="builds/:buildNumber"
                    element={<WorkspaceBuildPage />}
                  />
                  <Route path="settings" element={<WorkspaceSettingsLayout />}>
                    <Route index element={<WorkspaceSettingsPage />} />
                    <Route
                      path="parameters"
                      element={<WorkspaceParametersPage />}
                    />
                    <Route
                      path="schedule"
                      element={<WorkspaceSchedulePage />}
                    />
                  </Route>
                </Route>
              </Route>
            </Route>

            {/* Terminal and CLI auth pages don't have the dashboard layout */}
            <Route
              path="/:username/:workspace/terminal"
              element={<TerminalPage renderer="canvas" />}
            />
            <Route path="cli-auth" element={<CliAuthenticationPage />} />
          </Route>

          {/* Using path="*"" means "match anything", so this route
        acts like a catch-all for URLs that we don't have explicit
        routes for. */}
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </Router>
    </Suspense>
  )
}

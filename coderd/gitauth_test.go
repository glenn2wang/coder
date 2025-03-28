package coderd_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v43/github"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/coder/coder/coderd/coderdtest"
	"github.com/coder/coder/coderd/database"
	"github.com/coder/coder/coderd/gitauth"
	"github.com/coder/coder/coderd/httpapi"
	"github.com/coder/coder/codersdk"
	"github.com/coder/coder/codersdk/agentsdk"
	"github.com/coder/coder/provisioner/echo"
	"github.com/coder/coder/provisionersdk/proto"
	"github.com/coder/coder/testutil"
)

func TestGitAuthByID(t *testing.T) {
	t.Parallel()
	t.Run("Unauthenticated", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID:           "test",
				OAuth2Config: &testutil.OAuth2Config{},
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		auth, err := client.GitAuthByID(context.Background(), "test")
		require.NoError(t, err)
		require.False(t, auth.Authenticated)
	})
	t.Run("AuthenticatedNoUser", func(t *testing.T) {
		// Ensures that a provider that can't obtain a user can
		// still return that the provider is authenticated.
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID:           "test",
				OAuth2Config: &testutil.OAuth2Config{},
				// AzureDevops doesn't have a user endpoint!
				Type: codersdk.GitProviderAzureDevops,
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		resp := coderdtest.RequestGitAuthCallback(t, "test", client)
		_ = resp.Body.Close()
		auth, err := client.GitAuthByID(context.Background(), "test")
		require.NoError(t, err)
		require.True(t, auth.Authenticated)
	})
	t.Run("AuthenticatedWithUser", func(t *testing.T) {
		t.Parallel()
		validateSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpapi.Write(r.Context(), w, http.StatusOK, github.User{
				Login:     github.String("kyle"),
				AvatarURL: github.String("https://avatars.githubusercontent.com/u/12345678?v=4"),
			})
		}))
		defer validateSrv.Close()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID:           "test",
				ValidateURL:  validateSrv.URL,
				OAuth2Config: &testutil.OAuth2Config{},
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		resp := coderdtest.RequestGitAuthCallback(t, "test", client)
		_ = resp.Body.Close()
		auth, err := client.GitAuthByID(context.Background(), "test")
		require.NoError(t, err)
		require.True(t, auth.Authenticated)
		require.NotNil(t, auth.User)
		require.Equal(t, "kyle", auth.User.Login)
	})
	t.Run("AuthenticatedWithInstalls", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				httpapi.Write(r.Context(), w, http.StatusOK, github.User{
					Login:     github.String("kyle"),
					AvatarURL: github.String("https://avatars.githubusercontent.com/u/12345678?v=4"),
				})
			case "/installs":
				httpapi.Write(r.Context(), w, http.StatusOK, struct {
					Installations []github.Installation `json:"installations"`
				}{
					Installations: []github.Installation{{
						ID: github.Int64(12345678),
						Account: &github.User{
							Login: github.String("coder"),
						},
					}},
				})
			}
		}))
		defer srv.Close()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID:                  "test",
				ValidateURL:         srv.URL + "/user",
				AppInstallationsURL: srv.URL + "/installs",
				OAuth2Config:        &testutil.OAuth2Config{},
				Type:                codersdk.GitProviderGitHub,
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		resp := coderdtest.RequestGitAuthCallback(t, "test", client)
		_ = resp.Body.Close()
		auth, err := client.GitAuthByID(context.Background(), "test")
		require.NoError(t, err)
		require.True(t, auth.Authenticated)
		require.NotNil(t, auth.User)
		require.Equal(t, "kyle", auth.User.Login)
		require.NotNil(t, auth.AppInstallations)
		require.Len(t, auth.AppInstallations, 1)
	})
}

func TestGitAuthDevice(t *testing.T) {
	t.Parallel()
	t.Run("NotSupported", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID: "test",
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		_, err := client.GitAuthDeviceByID(context.Background(), "test")
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode())
	})
	t.Run("FetchCode", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpapi.Write(r.Context(), w, http.StatusOK, codersdk.GitAuthDevice{
				UserCode: "hey",
			})
		}))
		defer srv.Close()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID: "test",
				DeviceAuth: &gitauth.DeviceAuth{
					ClientID: "test",
					CodeURL:  srv.URL,
					Scopes:   []string{"repo"},
				},
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		device, err := client.GitAuthDeviceByID(context.Background(), "test")
		require.NoError(t, err)
		require.Equal(t, "hey", device.UserCode)
	})
	t.Run("ExchangeCode", func(t *testing.T) {
		t.Parallel()
		resp := gitauth.ExchangeDeviceCodeResponse{
			Error: "authorization_pending",
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpapi.Write(r.Context(), w, http.StatusOK, resp)
		}))
		defer srv.Close()
		client := coderdtest.New(t, &coderdtest.Options{
			GitAuthConfigs: []*gitauth.Config{{
				ID: "test",
				DeviceAuth: &gitauth.DeviceAuth{
					ClientID: "test",
					TokenURL: srv.URL,
					Scopes:   []string{"repo"},
				},
			}},
		})
		coderdtest.CreateFirstUser(t, client)
		err := client.GitAuthDeviceExchange(context.Background(), "test", codersdk.GitAuthDeviceExchange{
			DeviceCode: "hey",
		})
		var sdkErr *codersdk.Error
		require.ErrorAs(t, err, &sdkErr)
		require.Equal(t, http.StatusBadRequest, sdkErr.StatusCode())
		require.Equal(t, "authorization_pending", sdkErr.Detail)

		resp = gitauth.ExchangeDeviceCodeResponse{
			AccessToken: "hey",
		}

		err = client.GitAuthDeviceExchange(context.Background(), "test", codersdk.GitAuthDeviceExchange{
			DeviceCode: "hey",
		})
		require.NoError(t, err)

		auth, err := client.GitAuthByID(context.Background(), "test")
		require.NoError(t, err)
		require.True(t, auth.Authenticated)
	})
}

// nolint:bodyclose
func TestGitAuthCallback(t *testing.T) {
	t.Parallel()
	t.Run("NoMatchingConfig", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs:           []*gitauth.Config{},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		_, err := agentClient.GitAuth(context.Background(), "github.com", false)
		var apiError *codersdk.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, http.StatusNotFound, apiError.StatusCode())
	})
	t.Run("ReturnsURL", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.True(t, strings.HasSuffix(token.URL, fmt.Sprintf("/gitauth/%s", "github")))
	})
	t.Run("UnauthorizedCallback", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	})
	t.Run("AuthorizedCallback", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		_ = coderdtest.CreateFirstUser(t, client)
		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
		location, err := resp.Location()
		require.NoError(t, err)
		require.Equal(t, "/gitauth/github", location.Path)

		// Callback again to simulate updating the token.
		resp = coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	})
	t.Run("ValidateURL", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)

		srv := httptest.NewServer(nil)
		defer srv.Close()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				ValidateURL:  srv.URL,
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)

		// If the validation URL says unauthorized, the callback
		// URL to re-authenticate should be returned.
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		res, err := agentClient.GitAuth(ctx, "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, res.URL)

		// If the validation URL gives a non-OK status code, this
		// should be treated as an internal server error.
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("Something went wrong!"))
		})
		_, err = agentClient.GitAuth(ctx, "github.com/asd/asd", false)
		var apiError *codersdk.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, http.StatusInternalServerError, apiError.StatusCode())
		require.Equal(t, "validate git auth token: status 403: body: Something went wrong!", apiError.Detail)
	})

	t.Run("ExpiredNoRefresh", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{
					Token: &oauth2.Token{
						AccessToken:  "token",
						RefreshToken: "something",
						Expiry:       database.Now().Add(-time.Hour),
					},
				},
				ID:        "github",
				Regex:     regexp.MustCompile(`github\.com`),
				Type:      codersdk.GitProviderGitHub,
				NoRefresh: true,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, token.URL)

		// In the configuration, we set our OAuth provider
		// to return an expired token. Coder consumes this
		// and stores it.
		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)

		// Because the token is expired and `NoRefresh` is specified,
		// a redirect URL should be returned again.
		token, err = agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, token.URL)
	})

	t.Run("FullFlow", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, token.URL)

		// Start waiting for the token callback...
		tokenChan := make(chan agentsdk.GitAuthResponse, 1)
		go func() {
			token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", true)
			assert.NoError(t, err)
			tokenChan <- token
		}()

		time.Sleep(250 * time.Millisecond)

		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
		token = <-tokenChan
		require.Equal(t, "access_token", token.Username)

		token, err = agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
	})
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.cloudfoundry.org/cli/plugin"
	"github.com/contraband/autopilot/rewind"
)

type BgChangeStackPlugin struct{}
type Job struct {
	Metadata struct {
		GUID      string    `json:"guid"`
		CreatedAt time.Time `json:"created_at"`
		URL       string    `json:"url"`
	} `json:"metadata"`
	Entity struct {
		GUID         string `json:"guid"`
		Status       string `json:"status"`
		Error        string `json:"error"`
		ErrorDetails struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
			ErrorCode   string `json:"error_code"`
		} `json:"error_details"`
	} `json:"entity"`
}

func venerableAppName(appName string) string {
	return fmt.Sprintf("%s-venerable", appName)
}
func changeStackActions(appRepo *ApplicationRepo, appName string, newStackName string) []rewind.Action {
	return []rewind.Action{
		// create manifest
		{
			Forward: func() error {
				return appRepo.CreateManifest(appName)
			},
		},
		// create fake file to deploy
		{
			Forward: func() error {
				return appRepo.TouchDir()
			},
		},
		// rename
		{
			Forward: func() error {
				return appRepo.RenameApplication(appName, venerableAppName(appName))
			},
		},
		// push
		{
			Forward: func() error {
				appRepo.PushApplication(appName)
				return nil
			},
		},
		// Copy bits
		{
			Forward: func() error {
				oldAppGuid, err := appRepo.GetAppGuid(venerableAppName(appName))
				if err != nil {
					return err
				}
				newAppGuid, err := appRepo.GetAppGuid(appName)
				if err != nil {
					return err
				}
				job, err := appRepo.CopyBits(oldAppGuid, newAppGuid)
				if err != nil {
					return err
				}
				for {
					job, err := appRepo.GetJob(job.Entity.GUID)
					if err != nil {
						return err
					}
					if job.Entity.Status == "finished" {
						return nil
					}
					if job.Entity.Status == "failed" {
						return fmt.Errorf(
							"Error %s, %s [code: %d]",
							job.Entity.ErrorDetails.ErrorCode,
							job.Entity.ErrorDetails.Description,
							job.Entity.ErrorDetails.Code,
						)
					}
				}
				return nil
			},
			ReversePrevious: func() error {
				// If the app cannot start we'll have a lingering application
				// We delete this application so that the rename can succeed
				appRepo.DeleteApplication(appName)

				return appRepo.RenameApplication(venerableAppName(appName), appName)
			},
		},
		// restart
		{
			Forward: func() error {
				fmt.Println()
				return appRepo.RestartApplication(appName)
			},
			ReversePrevious: func() error {
				// If the app cannot start we'll have a lingering application
				// We delete this application so that the rename can succeed
				appRepo.DeleteApplication(appName)

				return appRepo.RenameApplication(venerableAppName(appName), appName)
			},
		},
		// change-stack
		{
			Forward: func() error {
				fmt.Println()
				newAppGuid, err := appRepo.GetAppGuid(appName)
				if err != nil {
					return err
				}

				return appRepo.AssignTargetStack(newAppGuid, newStackName)
			},
		},
		// Restage again for stack change to take effect
		{
			Forward: func() error {
				fmt.Println()
				return appRepo.RestageApplication(appName)
			},
			ReversePrevious: func() error {
				// If the app cannot start with new stack, we'll have a lingering application
				// We delete this application so that the rename can succeed
				appRepo.DeleteApplication(appName)

				return appRepo.RenameApplication(venerableAppName(appName), appName)
			},
		},
		// delete
		{
			Forward: func() error {
				return appRepo.DeleteApplication(venerableAppName(appName))
			},
		},
	}
}
func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stdout, "error:", err)
		os.Exit(1)
	}
}
func main() {
	plugin.Start(&BgChangeStackPlugin{})
}

func (plugin BgChangeStackPlugin) Run(cliConnection plugin.CliConnection, args []string) {

	switch args[0] {
	case "bg-change-stack":
		appRepo, err := NewApplicationRepo(cliConnection)
		fatalIf(err)
		defer appRepo.DeleteDir()
		if len(args) < 3 {
			fatalIf(fmt.Errorf("Usage: cf bg-change-stack <app name> <new stack name>"))
		}

		if args[0] == "bg-change-stack" {
			appName := args[1]
			actionList := changeStackActions(appRepo, appName, args[2])
			actions := rewind.Actions{
				Actions:              actionList,
				RewindFailureMessage: "Oh no. Something's gone wrong. I've tried to roll back but you should check to see if everything is OK.",
			}
			err = actions.Execute()
			fatalIf(err)

			fmt.Println()
			fmt.Println("application stack has been changed with no downtime !")
			fmt.Println()
		}
	case "CLI-MESSAGE-UNINSTALL":
		os.Exit(0)
	}
}

func (BgChangeStackPlugin) GetMetadata() plugin.PluginMetadata {
	return plugin.PluginMetadata{
		Name: "bg-change-stack",
		Version: plugin.VersionType{
			Major: 1,
			Minor: 0,
			Build: 0,
		},
		Commands: []plugin.Command{
			{
				Name:     "bg-change-stack",
				HelpText: "Perform a zero-downtime stack change of an application over the top of an old one",
				UsageDetails: plugin.Usage{
					Usage: "$ cf bg-change-stack <app name> <new stack name>",
				},
			},
		},
	}
}

type ApplicationRepo struct {
	conn plugin.CliConnection
	dir  string
}

func NewApplicationRepo(conn plugin.CliConnection) (*ApplicationRepo, error) {
	dir, err := ioutil.TempDir("", "bg-change-stack")
	if err != nil {
		return nil, err
	}
	return &ApplicationRepo{
		conn: conn,
		dir:  dir,
	}, nil
}

func (repo *ApplicationRepo) DeleteDir() error {
	return os.RemoveAll(repo.dir)
}

func (repo *ApplicationRepo) CreateManifest(name string) error {
	_, err := repo.conn.CliCommand("create-app-manifest", name, "-p", repo.manifestFilePath())
	return err
}

func (repo *ApplicationRepo) manifestFilePath() string {
	return filepath.Join(repo.dir, "manifest.yml")
}

func (repo *ApplicationRepo) TouchDir() error {
	f, err := os.Create(repo.dir + "/nofile")
	if err != nil {
		return err
	}

	defer f.Close()
	return nil
}

func (repo *ApplicationRepo) RenameApplication(oldName, newName string) error {
	_, err := repo.conn.CliCommand("rename", oldName, newName)
	return err
}

func (repo *ApplicationRepo) PushApplication(appName string) error {
	args := []string{"push", appName, "-f", repo.manifestFilePath(), "-p", repo.dir, "--no-start"}
	_, err := repo.conn.CliCommand(args...)
	return err
}

func (repo *ApplicationRepo) RestartApplication(appName string) error {
	args := []string{"restart", appName}
	_, err := repo.conn.CliCommand(args...)
	return err
}

func (repo *ApplicationRepo) RestageApplication(appName string) error {
	args := []string{"restage", appName}
	_, err := repo.conn.CliCommand(args...)
	return err
}

func (repo *ApplicationRepo) DeleteApplication(appName string) error {
	_, err := repo.conn.CliCommand("delete", appName, "-f")
	return err
}

func (repo *ApplicationRepo) ListApplications() error {
	_, err := repo.conn.CliCommand("apps")
	return err
}

func (repo *ApplicationRepo) CopyBits(oldAppGuid, newAppGuid string) (Job, error) {
	respSlice, err := repo.conn.CliCommandWithoutTerminalOutput(
		"curl",
		"-X",
		"POST",
		fmt.Sprintf("/v2/apps/%s/copy_bits", newAppGuid),
		"-d",
		fmt.Sprintf(`{"source_app_guid":"%s"}`, oldAppGuid),
	)
	if err != nil {
		return Job{}, err
	}
	resp := strings.Join(respSlice, "\n")
	var job Job
	err = json.Unmarshal([]byte(resp), &job)
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

func (repo *ApplicationRepo) AssignTargetStack(appGuid, stackName string) error {
	_, err := repo.conn.CliCommandWithoutTerminalOutput(
		"curl",
		"-X",
		"POST",
		"/v3/apps/"+appGuid, "-X", "PATCH", `-d={"lifecycle":{"type":"buildpack", "data": {"stack":"`+stackName+`"} } }`,
	)

	return err
}

func (repo *ApplicationRepo) GetJob(jobGuid string) (Job, error) {
	respSlice, err := repo.conn.CliCommandWithoutTerminalOutput(
		"curl",
		fmt.Sprintf("/v2/jobs/%s", jobGuid),
	)
	resp := strings.Join(respSlice, "\n")
	var job Job
	err = json.Unmarshal([]byte(resp), &job)
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

func (repo *ApplicationRepo) GetAppGuid(name string) (string, error) {
	d, err := repo.conn.CliCommandWithoutTerminalOutput("app", name, "--guid")
	if err != nil {
		return "", err
	}
	if len(d) == 0 {
		return "", fmt.Errorf("app '%s' not found.", name)
	}
	return d[0], err
}

func (repo *ApplicationRepo) DoesAppExist(appName string) (bool, error) {
	space, err := repo.conn.GetCurrentSpace()
	if err != nil {
		return false, err
	}

	path := fmt.Sprintf(`v2/apps?q=name:%s&q=space_guid:%s`, url.QueryEscape(appName), space.Guid)
	result, err := repo.conn.CliCommandWithoutTerminalOutput("curl", path)

	if err != nil {
		return false, err
	}

	jsonResp := strings.Join(result, "")

	output := make(map[string]interface{})
	err = json.Unmarshal([]byte(jsonResp), &output)

	if err != nil {
		return false, err
	}

	totalResults, ok := output["total_results"]

	if !ok {
		return false, errors.New("Missing total_results from api response")
	}

	count, ok := totalResults.(float64)

	if !ok {
		return false, fmt.Errorf("total_results didn't have a number %v", totalResults)
	}

	return count == 1, nil
}

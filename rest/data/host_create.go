package data

import (
	"fmt"
	"net/http"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/apimodels"
	"github.com/evergreen-ci/evergreen/cloud"
	"github.com/evergreen-ci/evergreen/model"
	"github.com/evergreen-ci/evergreen/model/distro"
	"github.com/evergreen-ci/evergreen/model/host"
	"github.com/evergreen-ci/evergreen/model/task"
	"github.com/evergreen-ci/evergreen/model/user"
	"github.com/evergreen-ci/gimlet"
	"github.com/mitchellh/mapstructure"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

// DBCreateHostConnector supports `host.create` commands from the agent.
type DBCreateHostConnector struct{}

// ListHostsForTask lists running hosts scoped to the task or the task's build.
func (dc *DBCreateHostConnector) ListHostsForTask(taskID string) ([]host.Host, error) {
	t, err := task.FindOneId(taskID)
	if err != nil {
		return nil, gimlet.ErrorResponse{StatusCode: http.StatusInternalServerError, Message: "error finding task"}
	}
	if t == nil {
		return nil, gimlet.ErrorResponse{StatusCode: http.StatusInternalServerError, Message: "no task found"}
	}

	catcher := grip.NewBasicCatcher()
	hostsSpawnedByTask, err := host.FindHostsSpawnedByTask(t.Id)
	catcher.Add(err)
	hostsSpawnedByBuild, err := host.FindHostsSpawnedByBuild(t.BuildId)
	catcher.Add(err)
	if catcher.HasErrors() {
		return nil, gimlet.ErrorResponse{StatusCode: http.StatusInternalServerError, Message: catcher.String()}
	}
	hosts := []host.Host{}
	for _, h := range hostsSpawnedByBuild {
		hosts = append(hosts, h)
	}
	for _, h := range hostsSpawnedByTask {
		hosts = append(hosts, h)
	}
	return hosts, nil
}

func (dc *DBCreateHostConnector) CreateHostsFromTask(t *task.Task, user user.DBUser, keyNameOrVal string) error {
	if t == nil {
		return errors.New("no task to create hosts from")
	}

	keyVal, err := user.GetPublicKey(keyNameOrVal)
	if err != nil {
		keyVal = keyNameOrVal
	}

	tc, err := model.MakeConfigFromTask(t)
	if err != nil {
		return err
	}

	projectTask := tc.Project.FindProjectTask(tc.Task.DisplayName)
	if projectTask == nil {
		return errors.Errorf("unable to find configuration for task %s", tc.Task.Id)
	}

	createHostCmds := []apimodels.CreateHost{}
	catcher := grip.NewBasicCatcher()
	for _, commandConf := range projectTask.Commands {
		if commandConf.Command != evergreen.CreateHostCommandName {
			continue
		}
		createHost := apimodels.CreateHost{}
		err = mapstructure.Decode(commandConf.Params, &createHost)
		if err != nil {
			return errors.New("error decoding createHost parameters")
		}
		createHostCmds = append(createHostCmds, createHost)
	}
	if catcher.HasErrors() {
		return catcher.Resolve()
	}

	hosts := []host.Host{}
	for _, createHost := range createHostCmds {
		for i := 0; i < createHost.NumHosts; i++ {
			intent, err := dc.MakeIntentHost(t.Id, user.Username(), keyVal, createHost)
			if err != nil {
				return errors.Wrap(err, "error creating host document")
			}
			hosts = append(hosts, *intent)
		}
	}

	return errors.Wrap(host.InsertMany(hosts), "error inserting host documents")
}

func (dc *DBCreateHostConnector) MakeIntentHost(taskID, userID, publicKey string, createHost apimodels.CreateHost) (*host.Host, error) {
	provider := evergreen.ProviderNameEc2OnDemand
	if createHost.Spot {
		provider = evergreen.ProviderNameEc2Spot
	}

	// get distro if it is set
	d := distro.Distro{}
	ec2Settings := cloud.EC2ProviderSettings{}
	var err error
	if distroID := createHost.Distro; distroID != "" {
		d, err = distro.FindOne(distro.ById(distroID))
		if err != nil {
			return nil, errors.Wrap(err, "problem finding distro")
		}
		if err := mapstructure.Decode(d.ProviderSettings, &ec2Settings); err != nil {
			return nil, errors.Wrap(err, "problem unmarshaling provider settings")
		}
	}

	// set provider
	d.Provider = provider

	if publicKey != "" {
		d.Setup += fmt.Sprintf("\necho \"\n%s\" >> ~%s/.ssh/authorized_keys\n", publicKey, d.User)
	}

	// set provider settings
	if createHost.AMI != "" {
		ec2Settings.AMI = createHost.AMI
	}
	if createHost.AWSKeyID != "" {
		ec2Settings.AWSKeyID = createHost.AWSKeyID
		ec2Settings.AWSSecret = createHost.AWSSecret
	}

	for _, mount := range createHost.EBSDevices {
		ec2Settings.MountPoints = append(ec2Settings.MountPoints, cloud.MountPoint{
			DeviceName: mount.DeviceName,
			Size:       int64(mount.SizeGiB),
			Iops:       int64(mount.IOPS),
			SnapshotID: mount.SnapshotID,
		})
	}
	if createHost.InstanceType != "" {
		ec2Settings.InstanceType = createHost.InstanceType
	}
	ec2Settings.KeyName = createHost.KeyName // never use the distro's key
	if createHost.Region != "" {
		ec2Settings.Region = createHost.Region
	}
	if len(createHost.SecurityGroups) > 0 {
		ec2Settings.SecurityGroupIDs = createHost.SecurityGroups
	}
	if createHost.Subnet != "" {
		ec2Settings.SubnetId = createHost.Subnet
	}
	if createHost.UserdataCommand != "" {
		ec2Settings.UserData = createHost.UserdataCommand
	}
	if createHost.VPC != "" {
		ec2Settings.VpcName = createHost.VPC
	}
	if err := mapstructure.Decode(ec2Settings, &d.ProviderSettings); err != nil {
		return nil, errors.Wrap(err, "error marshaling provider settings")
	}

	// scope and teardown options
	options := cloud.HostOptions{}
	if userID != "" {
		options.UserName = userID
		options.UserHost = true
		options.ProvisionOptions = &host.ProvisionOptions{
			LoadCLI: true,
			TaskId:  taskID,
			OwnerId: userID,
		}
	} else {
		options.UserName = taskID
		if createHost.Scope == "build" {
			t, err := task.FindOneId(taskID)
			if err != nil {
				return nil, errors.Wrap(err, "could not find task")
			}
			if t == nil {
				return nil, errors.New("no task returned")
			}
			options.SpawnOptions.BuildID = t.BuildId
		}
		if createHost.Scope == "task" {
			options.SpawnOptions.TaskID = taskID
		}
		options.SpawnOptions.TimeoutTeardown = time.Now().Add(time.Duration(createHost.TeardownTimeoutSecs) * time.Second)
		options.SpawnOptions.TimeoutSetup = time.Now().Add(time.Duration(createHost.SetupTimeoutSecs) * time.Second)
		options.SpawnOptions.Retries = createHost.Retries
		options.SpawnOptions.SpawnedByTask = true
	}

	return cloud.NewIntent(d, d.GenerateName(), provider, options), nil
}

// MockCreateHostConnector mocks `DBCreateHostConnector`.
type MockCreateHostConnector struct{}

// ListHostsForTask lists running hosts scoped to the task or the task's build.
func (*MockCreateHostConnector) ListHostsForTask(taskID string) ([]host.Host, error) {
	return nil, errors.New("method not implemented")
}

func (*MockCreateHostConnector) MakeIntentHost(taskID, userID, publicKey string, createHost apimodels.CreateHost) (*host.Host, error) {
	return nil, errors.New("MakeIntentHost not implemented")
}

func (*MockCreateHostConnector) CreateHostsFromTask(t *task.Task, user user.DBUser, keyNameOrVal string) error {
	return errors.New("CreateHostsFromTask not implemented")
}

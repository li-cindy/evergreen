package distro

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/util"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

type Distro struct {
	Id               string                  `bson:"_id" json:"_id,omitempty" mapstructure:"_id,omitempty"`
	Arch             string                  `bson:"arch" json:"arch,omitempty" mapstructure:"arch,omitempty"`
	WorkDir          string                  `bson:"work_dir" json:"work_dir,omitempty" mapstructure:"work_dir,omitempty"`
	PoolSize         int                     `bson:"pool_size,omitempty" json:"pool_size,omitempty" mapstructure:"pool_size,omitempty" yaml:"poolsize"`
	Provider         string                  `bson:"provider" json:"provider,omitempty" mapstructure:"provider,omitempty"`
	ProviderSettings *map[string]interface{} `bson:"settings" json:"settings,omitempty" mapstructure:"settings,omitempty"`

	SetupAsSudo bool     `bson:"setup_as_sudo,omitempty" json:"setup_as_sudo,omitempty" mapstructure:"setup_as_sudo,omitempty"`
	Setup       string   `bson:"setup,omitempty" json:"setup,omitempty" mapstructure:"setup,omitempty"`
	Teardown    string   `bson:"teardown,omitempty" json:"teardown,omitempty" mapstructure:"teardown,omitempty"`
	User        string   `bson:"user,omitempty" json:"user,omitempty" mapstructure:"user,omitempty"`
	SSHKey      string   `bson:"ssh_key,omitempty" json:"ssh_key,omitempty" mapstructure:"ssh_key,omitempty"`
	SSHOptions  []string `bson:"ssh_options,omitempty" json:"ssh_options,omitempty" mapstructure:"ssh_options,omitempty"`

	SpawnAllowed bool        `bson:"spawn_allowed" json:"spawn_allowed,omitempty" mapstructure:"spawn_allowed,omitempty"`
	Expansions   []Expansion `bson:"expansions,omitempty" json:"expansions,omitempty" mapstructure:"expansions,omitempty"`
	Disabled     bool        `bson:"disabled,omitempty" json:"disabled,omitempty" mapstructure:"disabled,omitempty"`

	ContainerPool string `bson:"container_pool,omitempty" json:"container_pool,omitempty" mapstructure:"container_pool,omitempty"`
}

type DistroGroup []Distro

type ValidateFormat string

type Expansion struct {
	Key   string `bson:"key,omitempty" json:"key,omitempty"`
	Value string `bson:"value,omitempty" json:"value,omitempty"`
}

// Seed the random number generator for creating distro names
func init() {
	rand.Seed(time.Now().UnixNano())
}

// GenerateName generates a unique instance name for a distro.
func (d *Distro) GenerateName() string {
	// gceMaxNameLength is the maximum length of an instance name permitted by GCE.
	const gceMaxNameLength = 63

	switch d.Provider {
	case evergreen.ProviderNameStatic:
		return "static"
	case evergreen.ProviderNameDocker:
		return fmt.Sprintf("container-%d", rand.New(rand.NewSource(time.Now().UnixNano())).Int())
	}

	name := fmt.Sprintf("evg-%s-%s-%d", d.Id, time.Now().Format(evergreen.NameTimeFormat), rand.Int())

	if d.Provider == evergreen.ProviderNameGce {
		// Ensure all characters in tags are on the whitelist
		r, _ := regexp.Compile("[^a-z0-9_-]+")
		name = string(r.ReplaceAll([]byte(strings.ToLower(name)), []byte("")))

		// Ensure the new name's is no longer than gceMaxNameLength
		if len(name) > gceMaxNameLength {
			name = name[:gceMaxNameLength]
		}
	}

	return name
}

func (d *Distro) IsWindows() bool {
	// XXX: if this is-windows check is updated, make sure to also update
	// public/static/js/spawned_hosts.js as well
	return strings.Contains(d.Arch, "windows")
}

func (d *Distro) IsEphemeral() bool {
	return util.StringSliceContains(evergreen.ProviderSpawnable, d.Provider)
}

func (d *Distro) BinaryName() string {
	name := "evergreen"
	if d.IsWindows() {
		return name + ".exe"
	}
	return name
}

// ExecutableSubPath returns the directory containing the compiled agents.
func (d *Distro) ExecutableSubPath() string {
	return filepath.Join(d.Arch, d.BinaryName())
}

// IsParent returns whether the distro is the parent distro for any container pool
func (d *Distro) IsParent(s *evergreen.Settings) bool {
	if s == nil {
		var err error
		s, err = evergreen.GetConfig()
		if err != nil {
			grip.Critical("error retrieving settings object")
			return false
		}
	}
	for _, p := range s.ContainerPools.Pools {
		if d.Id == p.Distro {
			return true
		}
	}
	return false
}

// ValidateContainerPoolDistros ensures that container pools have valid distros
func ValidateContainerPoolDistros(s *evergreen.Settings) error {
	catcher := grip.NewSimpleCatcher()

	for _, pool := range s.ContainerPools.Pools {
		d, err := FindOne(ById(pool.Distro))
		if err != nil {
			catcher.Add(fmt.Errorf("error finding distro for container pool %s", pool.Id))
		}
		if d.ContainerPool != "" {
			catcher.Add(fmt.Errorf("container pool %s has invalid distro", pool.Id))
		}
	}
	return errors.WithStack(catcher.Resolve())
}

// GetDistroIds returns a slice of distro IDs for the given group of distros
func (distros DistroGroup) GetDistroIds() []string {
	var ids []string
	for _, d := range distros {
		ids = append(ids, d.Id)
	}
	return ids
}

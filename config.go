package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
	"within.website/ln"
	"within.website/ln/opname"
)

type Config struct {
	InstanceID string   `yaml:"instance-id"`
	Hostname   string   `yaml:"hostname"`
	Users      []User   `yaml:"users,omitempty"`
	Files      []File   `yaml:"files,omitempty"`
	RunCommand []string `yaml:"runcmd,omitempty"`
}

func instanceIDSemaphore(id string) (bool, error) {
	os.MkdirAll("/var/cloud", 0700)
	sem := filepath.Join("/var/cloud", id)

	_, err := os.Stat(sem)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		fout, err := os.Create(sem)
		if err != nil {
			return false, fmt.Errorf("can't make %s: %w", sem, err)
		}
		fmt.Fprint(fout, id)
		err = fout.Close()
		if err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func changeHostname(ctx context.Context, hostname string) error {
	current, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("can't get current hostname: %w", err)
	}

	f := ln.F{"to": hostname, "from": current}

	if hostname == current {
		ln.Log(ctx, ln.Fmt("hostname matches target, doing nothing"), f)
	}

	ln.Log(ctx, f)
	st, err := os.Stat("/etc/hostname")
	if err != nil {
		return fmt.Errorf("can't query old hostname setting: %w", err)
	}
	err = os.Remove("/etc/hostname")
	if err != nil {
		return fmt.Errorf("can't remove old hostname setting: %w", err)
	}

	err = os.WriteFile("/etc/hostname", []byte(hostname), st.Mode())
	if err != nil {
		return fmt.Errorf("can't write new hostname: %w", err)
	}

	err = syscall.Sethostname([]byte(hostname))
	if err != nil {
		return fmt.Errorf("can't set hostname: %w", err)
	}

	return nil
}

func (c Config) Apply(ctx context.Context) error {
	if ok, err := instanceIDSemaphore(c.InstanceID); !ok || err != nil {
		if err != nil {
			return fmt.Errorf("error making instance id semaphore: %w", err)
		}
		ln.Log(ctx, ln.Fmt("already ran before, no reason to run now"))
		return nil
	}

	{
		ctx := opname.With(ctx, "hostname")
		err := changeHostname(ctx, c.Hostname)
		if err != nil {
			return err
		}
	}

	for _, u := range c.Users {
		ctx := opname.With(ctx, "mkuser")
		err := u.Apply(ctx)
		if err != nil {
			return err
		}
	}

	for _, f := range c.Files {
		ctx := opname.With(ctx, "mkfile")
		err := f.Apply(ctx)
		if err != nil {
			return err
		}
	}

	for _, cmd := range c.RunCommand {
		ctx := opname.With(ctx, "runcmd")
		err := run(ctx, "sh", "-c", cmd)
		if err != nil {
			return err
		}
	}

	return nil
}

func ParseConfig(r io.Reader) (Config, error) {
	var result Config
	err := yaml.NewDecoder(r).Decode(&result)
	return result, err
}

func run(ctx context.Context, name string, args ...string) error {
	ln.Log(ctx, ln.Fmt("running command"), ln.F{"name": name, "args": strings.Join(args, " ")})
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func makeUser(ctx context.Context, name, home, shell string, groups []string) error {
	groupList := strings.Join(groups, ",")
	err := run(ctx, "useradd", "-d", home, "-s", shell, "-m", "-U", "-G", groupList, name)
	if err != nil {
		ln.Error(ctx, err, ln.Fmt("error making user, are you on alpine?"))
		err := run(ctx, "adduser", "-h", home, "-s", shell, "-D", name)
		if err != nil {
			return err
		}
		for _, g := range groups {
			err := run(ctx, "adduser", name, g)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type User struct {
	Name           string   `yaml:"name"`
	Home           string   `yaml:"home"`
	Groups         []string `yaml:"groups"`
	Sudo           []string `yaml:"sudo"`
	Shell          string   `yaml:"shell"`
	AuthorizedKeys []string `yaml:"ssh-authorized-keys"`
	GitHub         string   `yaml:"github"`
}

func (u User) F() ln.F {
	return ln.F{
		"name":   u.Name,
		"home":   u.Home,
		"groups": u.Groups,
		"shell":  u.Shell,
		"github": u.GitHub,
	}
}

func (u User) Apply(ctx context.Context) error {
	if u.Home == "" {
		u.Home = filepath.Join("/", "home", u.Name)
	}

	if u.Shell == "" {
		u.Shell = "/bin/sh"
	}

	ln.Log(ctx, ln.Fmt("making user"), u)
	err := makeUser(ctx, u.Name, u.Home, u.Shell, u.Groups)
	if err != nil {
		return err
	}

	return nil
}

type File struct {
	Path        string `yaml:"path"`
	Permissions string `yaml:"permissions"`
	Contents    string `yaml:"contents"`
	Owner       string `yaml:"owner"`
	Group       string `yaml:"group"`
}

func (f File) F() ln.F {
	return ln.F{
		"path":  f.Path,
		"perms": f.Permissions,
		"owner": f.Owner,
		"group": f.Group,
	}
}

func (f File) Apply(ctx context.Context) error {
	ln.Log(ctx, ln.Fmt("making file"), f)
	perm, err := strconv.ParseUint(f.Permissions, 8, 32)
	if err != nil {
		return fmt.Errorf("can't read permissions %s: %w", f.Permissions, err)
	}

	fout, err := os.OpenFile(f.Path, os.O_CREATE|os.O_WRONLY, fs.FileMode(perm))
	if err != nil {
		return err
	}
	fmt.Fprint(fout, f.Contents)
	defer fout.Close()

	uid, err := user.Lookup(f.Owner)
	if err != nil {
		return fmt.Errorf("can't find user %s: %w", f.Owner, err)
	}

	gid, err := user.LookupGroup(f.Group)
	if err != nil {
		return fmt.Errorf("can't find group %s: %w", f.Owner, err)
	}

	err = os.Chown(f.Path, mustAtoi(uid.Uid), mustAtoi(gid.Gid))
	if err != nil {
		return fmt.Errorf("can't chown to %s:%s (%s:%s): %w", uid.Uid, gid.Gid, uid.Username, gid.Name, err)
	}

	return nil
}

func mustAtoi(s string) int {
	result, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}

	return result
}

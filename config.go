package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// Config is the tool's settings file, $XDG_CONFIG_HOME/o-qbt/config.json
// (else ~/.config/o-qbt/config.json), mode 600:
//
//	{"url": "http://nas:8012", "user": "admin", "pass": "", "volume": "/volume1", "ssh": "nas"}
//
// O_QBT_URL, O_QBT_USER and O_QBT_PASS override the file. pass is never
// printed and never read from argv.
type Config struct {
	URL    string `json:"url"`
	User   string `json:"user"`
	Pass   string `json:"pass"`
	Volume string `json:"volume"` // the NAS path guard measures with df
	SSH    string `json:"ssh"`    // the ssh alias guard runs df through
}

// DefaultConfig is what config init writes.
var DefaultConfig = Config{URL: "http://nas:8012", User: "admin", Volume: "/volume1", SSH: "nas"}

// configKeys are the keys config set takes.
var configKeys = []string{"url", "user", "pass", "volume", "ssh"}

// ConfigView is config show's answer: the effective settings without the
// password, where each came from, and whether a password is set.
type ConfigView struct {
	Path    string            `json:"path"`
	Exists  bool              `json:"exists"`
	URL     string            `json:"url"`
	User    string            `json:"user"`
	PassSet bool              `json:"pass_set"`
	Volume  string            `json:"volume"`
	SSH     string            `json:"ssh"`
	Env     map[string]string `json:"env_overrides,omitempty"` // key -> the variable that set it
}

func (v ConfigView) Text() string {
	pass := "not set (o qbt config set pass)"
	if v.PassSet {
		pass = "set"
	}
	exists := ""
	if !v.Exists {
		exists = " (missing: o qbt config init)"
	}
	rows := [][2]string{{"path", v.Path + exists}, {"url", v.URL}, {"user", v.User}, {"pass", pass}, {"volume", v.Volume}, {"ssh", v.SSH}}
	for _, k := range configKeys {
		if env := v.Env[k]; env != "" {
			rows = append(rows, [2]string{"override", k + " from $" + env})
		}
	}
	return table(rows)
}

// ConfigPath is the settings file.
func (t *Tool) ConfigPath() string {
	base := t.getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(t.Home, ".config")
	}
	return filepath.Join(base, "o-qbt", "config.json")
}

// readConfigFile is the file's settings; a missing file is (zero, false, nil).
func (t *Tool) readConfigFile() (Config, bool, error) {
	var c Config
	data, err := os.ReadFile(t.ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, true, fmt.Errorf("%s: %w", t.ConfigPath(), err)
	}
	return c, true, nil
}

var envKeys = map[string]string{"url": "O_QBT_URL", "user": "O_QBT_USER", "pass": "O_QBT_PASS"}

// config is the effective settings: the file (defaults where it is
// missing or a field is empty, the password aside) under the environment's
// overrides, and which keys the environment set.
func (t *Tool) config() (Config, map[string]string, bool, error) {
	c, exists, err := t.readConfigFile()
	if err != nil {
		return c, nil, exists, err
	}
	for _, f := range []struct {
		dst *string
		def string
	}{{&c.URL, DefaultConfig.URL}, {&c.User, DefaultConfig.User}, {&c.Volume, DefaultConfig.Volume}, {&c.SSH, DefaultConfig.SSH}} {
		if *f.dst == "" {
			*f.dst = f.def
		}
	}
	env := map[string]string{}
	for key, name := range envKeys {
		if v := t.getenv(name); v != "" {
			*c.field(key) = v
			env[key] = name
		}
	}
	c.URL = strings.TrimRight(c.URL, "/")
	return c, env, exists, nil
}

func (c *Config) field(key string) *string {
	switch key {
	case "url":
		return &c.URL
	case "user":
		return &c.User
	case "pass":
		return &c.Pass
	case "volume":
		return &c.Volume
	case "ssh":
		return &c.SSH
	}
	return nil
}

// writeConfig writes c to the settings file, mode 600, atomically.
func (t *Tool) writeConfig(c Config) error {
	path := t.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Message is a verb's answer for a person: one or more lines.
type Message struct {
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

func (m Message) Text() string { return m.Message + "\n" }

// ConfigInit writes the default settings with an empty password unless
// the file exists, and names it.
func (t *Tool) ConfigInit() (Message, error) {
	path := t.ConfigPath()
	if _, exists, err := t.readConfigFile(); err != nil || exists {
		if err != nil {
			return Message{}, err
		}
		return Message{Message: path + " exists; left unchanged (o qbt config show)", Path: path}, os.Chmod(path, 0o600)
	}
	if err := t.writeConfig(DefaultConfig); err != nil {
		return Message{}, err
	}
	return Message{Message: path + "\nnext: o qbt config set pass, then o qbt app info", Path: path}, nil
}

// ConfigShow is the effective settings, never the password.
func (t *Tool) ConfigShow() (ConfigView, error) {
	c, env, exists, err := t.config()
	if err != nil {
		return ConfigView{}, err
	}
	if len(env) == 0 {
		env = nil
	}
	return ConfigView{Path: t.ConfigPath(), Exists: exists, URL: c.URL, User: c.User, PassSet: c.Pass != "", Volume: c.Volume, SSH: c.SSH, Env: env}, nil
}

// ConfigSet sets one key in the file. pass never comes from value: it is
// read from the terminal with echo off, or one line of stdin with
// fromStdin.
func (t *Tool) ConfigSet(key, value string, fromStdin bool) (Message, error) {
	c, _, err := t.readConfigFile()
	if err != nil {
		return Message{}, err
	}
	if c.field(key) == nil {
		return Message{}, fmt.Errorf("config set: unknown key %q (%s)", key, strings.Join(configKeys, ", "))
	}
	if c == (Config{}) {
		c = DefaultConfig
	}
	if key == "pass" {
		if value != "" {
			return Message{}, errors.New("config set pass takes no value on the command line (it would land in shell history); it prompts, or reads one line with --stdin")
		}
		if value, err = t.readSecret(fromStdin); err != nil {
			return Message{}, err
		}
		if value == "" {
			return Message{}, errors.New("config set pass: empty password; nothing written")
		}
	} else {
		if fromStdin {
			if value, err = readLine(t.Stdin); err != nil {
				return Message{}, err
			}
		}
		if value == "" {
			return Message{}, fmt.Errorf("config set %s needs VALUE", key)
		}
	}
	*c.field(key) = value
	if err := t.writeConfig(c); err != nil {
		return Message{}, err
	}
	shown := value
	if key == "pass" {
		shown = "(hidden)"
	}
	return Message{Message: fmt.Sprintf("set %s = %s in %s", key, shown, t.ConfigPath()), Path: t.ConfigPath()}, nil
}

// readSecret is a password: one line of stdin with fromStdin, else a
// prompt on stderr read from the terminal with echo off.
func (t *Tool) readSecret(fromStdin bool) (string, error) {
	if fromStdin {
		return readLine(t.Stdin)
	}
	f, ok := t.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", errors.New("config set pass: stdin is not a terminal; pipe the password with --stdin")
	}
	fmt.Fprint(t.Stderr, "qBittorrent password: ")
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(t.Stderr)
	return strings.TrimRight(string(b), "\r\n"), err
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

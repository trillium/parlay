// Command validate checks packages/spawn-profiles/profiles.toml for shape
// errors and reports one precise line per problem. Exit 0 means well-formed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

type profile struct {
	Name               string            `toml:"name"`
	DisplayName        string            `toml:"display_name"`
	Kind               string            `toml:"kind"`
	Command            string            `toml:"command"`
	Model              string            `toml:"model"`
	Args               []string          `toml:"args"`
	PromptMode         string            `toml:"prompt_mode"`
	PromptFlag         string            `toml:"prompt_flag"`
	ResumeFlag         string            `toml:"resume_flag"`
	ResumeStyle        string            `toml:"resume_style"`
	SessionIDFlag      string            `toml:"session_id_flag"`
	ReadyDelayMs       int               `toml:"ready_delay_ms"`
	ReadyPromptPrefix  string            `toml:"ready_prompt_prefix"`
	Env                map[string]string `toml:"env"`
}

type profilesFile struct {
	Profile []profile `toml:"profile"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var promptModes = map[string]bool{"arg": true, "flag": true, "none": true}
var resumeStyles = map[string]bool{"": true, "flag": true, "subcommand": true}

func main() {
	path := "profiles.toml"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	var f profilesFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		fmt.Printf("%s: cannot parse: %v\n", path, err)
		os.Exit(1)
	}

	var errs []string
	seen := map[string]bool{}
	for i, p := range f.Profile {
		label := p.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		bad := func(msg string) {
			errs = append(errs, fmt.Sprintf("profile %q: %s", label, msg))
		}

		if p.Name == "" {
			bad("missing required field name")
		} else if !nameRe.MatchString(p.Name) {
			bad("name must be a kebab id matching [a-z0-9][a-z0-9-]*")
		} else if seen[p.Name] {
			bad("duplicate name")
		}
		seen[p.Name] = true

		if strings.TrimSpace(p.Kind) == "" {
			bad("missing required field kind")
		}
		if strings.TrimSpace(p.Command) == "" {
			bad("missing required field command")
		}
		if !promptModes[p.PromptMode] {
			bad(fmt.Sprintf("prompt_mode must be one of arg|flag|none (got %q)", p.PromptMode))
		}
		if p.PromptMode == "flag" && strings.TrimSpace(p.PromptFlag) == "" {
			bad(`prompt_mode "flag" requires prompt_flag (e.g. "--prompt")`)
		}
		if p.PromptMode != "flag" && p.PromptFlag != "" {
			bad("prompt_flag set but prompt_mode is not \"flag\"")
		}
		if !resumeStyles[p.ResumeStyle] {
			bad(fmt.Sprintf("resume_style must be one of flag|subcommand (got %q)", p.ResumeStyle))
		}
		if p.ReadyDelayMs < 0 {
			bad("ready_delay_ms must be >= 0")
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Println(e)
		}
		os.Exit(1)
	}

	fmt.Printf("%s: %d profiles valid\n", filepath.Base(path), len(f.Profile))
}

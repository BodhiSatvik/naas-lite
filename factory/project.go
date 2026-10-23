package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Project moves a ticket's card on a GitHub Project board. It is optional:
// the controller works with labels alone. Turn it on with -project
// OWNER/NUMBER after "factory init -project OWNER/NUMBER" created the
// "Factory Stage" field. Needs a gh token with the project scope:
//
//	gh auth refresh -s project
type Project struct {
	gh      *GH
	Owner   string
	Number  string
	ID      string
	FieldID string
	Options map[string]string // lower-case option name -> option id
	items   sync.Map          // issue URL -> project item id
}

func parseProjectSpec(spec string) (owner, number string, err error) {
	i := strings.LastIndex(spec, "/")
	if i <= 0 || i == len(spec)-1 {
		return "", "", fmt.Errorf("project must look like OWNER/NUMBER (use @me for your user), got %q", spec)
	}
	return spec[:i], spec[i+1:], nil
}

type projectField struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Options []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"options"`
}

func listProjectFields(ctx context.Context, g *GH, owner, number string) ([]projectField, error) {
	out, err := g.run(ctx, "project", "field-list", number, "--owner", owner, "--format", "json", "--limit", "100")
	if err != nil {
		return nil, err
	}
	var fl struct {
		Fields []projectField `json:"fields"`
	}
	if err := json.Unmarshal(out, &fl); err != nil {
		return nil, fmt.Errorf("parse project fields: %w", err)
	}
	return fl.Fields, nil
}

func loadProject(ctx context.Context, g *GH, spec string) (*Project, error) {
	owner, number, err := parseProjectSpec(spec)
	if err != nil {
		return nil, err
	}
	out, err := g.run(ctx, "project", "view", number, "--owner", owner, "--format", "json")
	if err != nil {
		return nil, err
	}
	var pv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &pv); err != nil || pv.ID == "" {
		return nil, fmt.Errorf("cannot read project %s", spec)
	}
	fields, err := listProjectFields(ctx, g, owner, number)
	if err != nil {
		return nil, err
	}
	p := &Project{gh: g, Owner: owner, Number: number, ID: pv.ID, Options: map[string]string{}}
	for _, f := range fields {
		if strings.EqualFold(f.Name, projectFieldName) {
			p.FieldID = f.ID
			for _, o := range f.Options {
				p.Options[strings.ToLower(o.Name)] = o.ID
			}
		}
	}
	if p.FieldID == "" {
		return nil, fmt.Errorf("project %s has no %q field; run: factory init -project %s", spec, projectFieldName, spec)
	}
	return p, nil
}

// SetStage adds the issue to the project (once) and sets its Factory Stage.
func (p *Project) SetStage(ctx context.Context, issueURL string, s Stage) error {
	opt := p.Options[strings.ToLower(projectOption(s))]
	if opt == "" || issueURL == "" {
		return nil
	}
	item, ok := p.items.Load(issueURL)
	if !ok {
		out, err := p.gh.write(ctx, "project", "item-add", p.Number, "--owner", p.Owner, "--url", issueURL, "--format", "json")
		if err != nil {
			return err
		}
		var it struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out, &it); err != nil || it.ID == "" {
			return nil // dry-run, or an unexpected response
		}
		p.items.Store(issueURL, it.ID)
		item = it.ID
	}
	_, err := p.gh.write(ctx, "project", "item-edit", "--id", item.(string), "--project-id", p.ID,
		"--field-id", p.FieldID, "--single-select-option-id", opt)
	return err
}

// createProjectField adds the "Factory Stage" single-select field.
func createProjectField(ctx context.Context, g *GH, spec string) error {
	owner, number, err := parseProjectSpec(spec)
	if err != nil {
		return err
	}
	fields, err := listProjectFields(ctx, g, owner, number)
	if err != nil {
		return err
	}
	for _, f := range fields {
		if strings.EqualFold(f.Name, projectFieldName) {
			fmt.Printf("project field %q already exists\n", projectFieldName)
			return nil
		}
	}
	_, err = g.write(ctx, "project", "field-create", number, "--owner", owner,
		"--name", projectFieldName, "--data-type", "SINGLE_SELECT",
		"--single-select-options", strings.Join(projectOptions, ","))
	if err == nil {
		fmt.Printf("created project field %q - set your board view's column field to it\n", projectFieldName)
	}
	return err
}

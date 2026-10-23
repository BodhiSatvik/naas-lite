package main

import "fmt"

// Stage is one step in a ticket's life. Each stage has a GitHub label, so the
// issue itself always shows where the ticket is on the line.
type Stage string

const (
	StageTriage    Stage = "triage"
	StageSpec      Stage = "spec"
	StageApproval  Stage = "awaiting-approval"
	StageBuild     Stage = "build"
	StageVerify    Stage = "verify"
	StageReview    Stage = "review"
	StageShip      Stage = "ship"
	StageShipped   Stage = "shipped"
	StageNeedsInfo Stage = "needs-info"
	StageFailed    Stage = "failed"
)

const (
	labelPrefix   = "factory:"
	labelApproved = "factory:approved"
)

// Label is the GitHub label that marks a ticket in this stage.
func (s Stage) Label() string { return labelPrefix + string(s) }

// allStages lists every stage label the controller owns. The controller keeps
// exactly one of them on an issue at a time.
var allStages = []Stage{
	StageTriage, StageSpec, StageApproval, StageBuild, StageVerify,
	StageReview, StageShip, StageShipped, StageNeedsInfo, StageFailed,
}

func isTerminal(s Stage) bool {
	return s == StageShipped || s == StageNeedsInfo || s == StageFailed
}

type labelDef struct{ Name, Color, Desc string }

// labelDefs returns every label the factory uses, with colors that read well
// on a GitHub board.
func labelDefs(intake string) []labelDef {
	return []labelDef{
		{intake, "5319e7", "Put this ticket on the AI factory line"},
		{StageTriage.Label(), "c5def5", "Factory: triage station is running"},
		{StageSpec.Label(), "bfd4f2", "Factory: spec station is running"},
		{StageApproval.Label(), "fbca04", "Factory: spec is waiting for human approval"},
		{labelApproved, "0e8a16", "Factory: a human approved the spec"},
		{StageBuild.Label(), "d4c5f9", "Factory: build station is running"},
		{StageVerify.Label(), "c2e0c6", "Factory: verify gate is running"},
		{StageReview.Label(), "f9d0c4", "Factory: review station is running"},
		{StageShip.Label(), "bfdadc", "Factory: ship station is running"},
		{StageShipped.Label(), "1d76db", "Factory: pull request is open"},
		{StageNeedsInfo.Label(), "d93f0b", "Factory: triage needs more information"},
		{StageFailed.Label(), "b60205", "Factory: stopped, needs a human"},
	}
}

// projectFieldName is the single-select field on a GitHub Project that the
// controller moves as the ticket advances. Group a board view by it.
const projectFieldName = "Factory Stage"

var projectOptions = []string{
	"Triage", "Spec", "Awaiting approval", "Build", "Verify",
	"Review", "Ship", "Shipped", "Needs info", "Failed",
}

func projectOption(s Stage) string {
	switch s {
	case StageTriage:
		return "Triage"
	case StageSpec:
		return "Spec"
	case StageApproval:
		return "Awaiting approval"
	case StageBuild:
		return "Build"
	case StageVerify:
		return "Verify"
	case StageReview:
		return "Review"
	case StageShip:
		return "Ship"
	case StageShipped:
		return "Shipped"
	case StageNeedsInfo:
		return "Needs info"
	case StageFailed:
		return "Failed"
	}
	return ""
}

// stationNumber is the station's position on the line, 1 to 6. It is 0 for
// stages that are not stations (the gate and the terminal states).
func stationNumber(s Stage) int {
	switch s {
	case StageTriage:
		return 1
	case StageSpec:
		return 2
	case StageBuild:
		return 3
	case StageVerify:
		return 4
	case StageReview:
		return 5
	case StageShip:
		return 6
	}
	return 0
}

// stationName labels one run of a station, for example "3·build" or
// "3·build #2" for a retry. It is the herdr tab name.
func stationName(s Stage, attempt int) string {
	name := fmt.Sprintf("%d·%s", stationNumber(s), s)
	if attempt > 1 {
		name += fmt.Sprintf(" #%d", attempt)
	}
	return name
}

// stationTools is the least-privilege tool allowlist for each AI station. The
// controller passes it to claude --allowedTools. The hard limits that apply to
// every station (no push, no gh, no kubectl apply, no edits to factory/ or
// .claude/) live in .claude/settings.json as deny rules.
func stationTools(s Stage, memoryMCP string) []string {
	tools := []string{
		"Read", "Grep", "Glob", "Write", "Edit",
		"Bash(git log *)", "Bash(git diff *)", "Bash(git show *)", "Bash(git status *)",
	}
	if memoryMCP != "" {
		tools = append(tools, fmt.Sprintf("mcp__%s__*", memoryMCP))
	}
	switch s {
	case StageBuild:
		tools = append(tools,
			"Bash(go test *)", "Bash(go vet *)", "Bash(go build *)", "Bash(gofmt *)",
			"Bash(go fmt *)", "Bash(go mod tidy)", "Bash(make test)", "Bash(make vet)",
			"Bash(make fmt)", "Bash(make verify)", "Bash(git add *)", "Bash(git commit *)")
	case StageReview:
		tools = append(tools, "Bash(go test *)", "Bash(go vet *)")
	}
	return tools
}

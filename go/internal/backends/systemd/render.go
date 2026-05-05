package systemd

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/policy"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

var unitTemplates = template.Must(template.ParseFS(templatesFS, "templates/*.tmpl"))

// renderVars is the data the unit templates render against. Computed
// once per (job, scheduleIndex); unused fields are set to zero values
// so the timer template doesn't need to know about TriggerBin and
// vice versa.
type renderVars struct {
	App           string
	Job           string
	Index         int
	UnitName      string
	OnCalendar    string
	Hash          string
	TriggerBin    string
	RuntimeMaxSec int
}

// RenderUnits returns the (.timer, .service) file contents for one
// schedule of the given job. The hash field is left empty — useful for
// `RenderUnits` callers that want the unit shape without the change-
// detection annotation.
func RenderUnits(triggerBin, app string, job manifest.NormalizedJob, idx int) (timerFile, serviceFile string, err error) {
	return renderUnitsWithHash(triggerBin, app, job, idx, "")
}

func renderUnitsWithHash(triggerBin, app string, job manifest.NormalizedJob, idx int, hash string) (timerFile, serviceFile string, err error) {
	if idx < 0 || idx >= len(job.Schedules) {
		return "", "", fmt.Errorf("systemd: schedule index %d out of range (have %d)", idx, len(job.Schedules))
	}
	cal, err := translateOnCalendar(job.Schedules[idx])
	if err != nil {
		return "", "", err
	}
	vars := renderVars{
		App:           app,
		Job:           job.Name,
		Index:         idx,
		UnitName:      policy.ScheduleName(app, job.Name, idx),
		OnCalendar:    cal,
		Hash:          hash,
		TriggerBin:    triggerBin,
		RuntimeMaxSec: job.Policy.TimeoutSeconds + timeoutHeadroomSeconds,
	}
	timerFile, err = renderTemplate("timer.tmpl", vars)
	if err != nil {
		return "", "", err
	}
	serviceFile, err = renderTemplate("service.tmpl", vars)
	if err != nil {
		return "", "", err
	}
	return timerFile, serviceFile, nil
}

func renderTemplate(name string, vars renderVars) (string, error) {
	var buf bytes.Buffer
	if err := unitTemplates.ExecuteTemplate(&buf, name, vars); err != nil {
		return "", fmt.Errorf("systemd: render %s: %w", name, err)
	}
	return buf.String(), nil
}

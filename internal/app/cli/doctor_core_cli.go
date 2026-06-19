package cli

import (
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

func renderDoctorCoreText(report doctor.Report) string {
	var b strings.Builder
	b.WriteString("podlaz core diagnostics\n")
	for _, check := range report.Checks {
		fmt.Fprintf(&b, "[%s] %s: %s\n", check.Severity, render.Redact(check.Name), render.Redact(check.Message))
	}
	return b.String()
}

type doctorJSONResponse struct {
	SchemaVersion string            `json:"schema_version"`
	Status        string            `json:"status"`
	Warnings      []string          `json:"warnings"`
	Errors        []string          `json:"errors"`
	Source        string            `json:"source"`
	Checks        []doctorJSONCheck `json:"checks"`
}

type doctorJSONCheck struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func doctorJSON(report doctor.Report) doctorJSONResponse {
	response := doctorJSONResponse{
		SchemaVersion: "v1",
		Status:        doctorStatus(report),
		Warnings:      doctorMessages(report, doctor.SeverityWarning),
		Errors:        doctorMessages(report, doctor.SeverityFail),
		Source:        render.Redact(report.Source),
		Checks:        make([]doctorJSONCheck, 0, len(report.Checks)),
	}
	for _, check := range report.Checks {
		response.Checks = append(response.Checks, doctorJSONCheck{
			Name:     render.Redact(check.Name),
			Severity: string(check.Severity),
			Message:  render.Redact(check.Message),
		})
	}
	return response
}

func doctorStatus(report doctor.Report) string {
	if report.HasFailures() {
		return "fail"
	}
	for _, check := range report.Checks {
		if check.Severity == doctor.SeverityWarning {
			return "warn"
		}
	}
	return "ok"
}

func doctorMessages(report doctor.Report, severity doctor.Severity) []string {
	messages := make([]string, 0)
	for _, check := range report.Checks {
		if check.Severity == severity {
			messages = append(messages, render.Redact(check.Message))
		}
	}
	return messages
}

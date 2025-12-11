package replay

import "github.com/happydev123/backendlab/internal/finding"

// Codes for findings that are not transport errors or assertions.
const (
	CodeDependencyUnavailable = "replay.dependency_unavailable"
	CodeValidationUnavailable = "replay.validation_unavailable"
	CodeStreamUnavailable     = "replay.stream_unavailable"
	CodeCompareUnavailable    = "replay.compare_unavailable"
	CodeJSONOnStream          = "replay.json_on_stream"
)

// newFinding builds a replay finding for one case and target. Every
// string must already be sanitized.
func newFinding(kind finding.Kind, sev finding.Severity, code, caseID, target, location, msg string) finding.Finding {
	f := finding.Finding{
		Module:   finding.ModuleReplay,
		Code:     code,
		Kind:     kind,
		Severity: sev,
		CaseID:   caseID,
		Location: location,
		Message:  msg,
	}
	if target != "" {
		f.Details = map[string]string{"target": target}
	}
	return f
}

// transportError reports that no complete response was obtained.
func transportError(code, caseID, target, msg string) finding.Finding {
	return newFinding(finding.KindTransportError, finding.SeverityError, code, caseID, target, "", msg)
}

// inconclusive reports a check that could not be performed.
func inconclusive(code, caseID, target, msg string) finding.Finding {
	return newFinding(finding.KindInconclusive, finding.SeverityWarning, code, caseID, target, "", msg)
}

// unsupported reports a suite feature this execution cannot evaluate.
func unsupported(code, caseID, target, msg string) finding.Finding {
	return newFinding(finding.KindUnsupported, finding.SeverityWarning, code, caseID, target, "", msg)
}

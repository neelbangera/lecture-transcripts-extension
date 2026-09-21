package protocol

import (
	"encoding/json"
	"testing"
)

func readSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(readProtocolFile(t, name), &schema); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return schema
}

func defEntry(t *testing.T, schema map[string]any, def string) map[string]any {
	t.Helper()
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no $defs object")
	}
	entry, ok := defs[def].(map[string]any)
	if !ok {
		t.Fatalf("schema has no $defs.%s object", def)
	}
	return entry
}

func defEnum(t *testing.T, schema map[string]any, def string) []string {
	t.Helper()
	raw, ok := defEntry(t, schema, def)["enum"].([]any)
	if !ok {
		t.Fatalf("$defs.%s has no enum", def)
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("$defs.%s enum contains non-string %v", def, value)
		}
		values = append(values, text)
	}
	return values
}

func oneOfEntry(t *testing.T, schema map[string]any, title string) map[string]any {
	t.Helper()
	entries, ok := schema["oneOf"].([]any)
	if !ok {
		t.Fatalf("schema has no oneOf array")
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if entry["title"] == title {
			return entry
		}
	}
	t.Fatalf("schema oneOf has no %s entry", title)
	return nil
}

func objectKeys(t *testing.T, object map[string]any) []string {
	t.Helper()
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return keys
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func assertSameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSet := stringSet(got)
	wantSet := stringSet(want)
	for value := range wantSet {
		if _, ok := gotSet[value]; !ok {
			t.Fatalf("%s is missing %q: got %v want %v", label, value, got, want)
		}
	}
	for value := range gotSet {
		if _, ok := wantSet[value]; !ok {
			t.Fatalf("%s has unexpected %q: got %v want %v", label, value, got, want)
		}
	}
}

func TestSchemaVocabularyMatchesGo(t *testing.T) {
	schema := readSchema(t, "native-messaging.schema.json")

	queueStatuses := []string{
		string(StatusQueued), string(StatusUploading), string(StatusUploaded),
		string(StatusUnchanged), string(StatusRetryableError),
		string(StatusPermanentConflict), string(StatusRejectedMissingIdentity),
		string(StatusRejectedAmbiguousMetadata), string(StatusRejectedOversized),
		string(StatusRejectedQueueFull), string(StatusRejectedInvalidHash),
		string(StatusRejectedUnsafeURL), string(StatusRejectedUnknownField),
		string(StatusRejectedInvalidSchema), string(StatusRejectedPermission),
	}
	assertSameSet(t, "queueStatus", defEnum(t, schema, "queueStatus"), queueStatuses)
	for _, status := range queueStatuses {
		if !IsQueueStatus(QueueStatus(status)) {
			t.Fatalf("IsQueueStatus(%q) = false", status)
		}
	}
	if IsQueueStatus("bogus") {
		t.Fatal("IsQueueStatus(bogus) = true")
	}

	errorCategories := []string{
		string(ErrorProtocolMismatch), string(ErrorInvalidMessage),
		string(ErrorHostUnavailable), string(ErrorInvalidState),
		string(ErrorNotConnected), string(ErrorReauthorizationRequired),
		string(ErrorTargetRepositoryUnavailable), string(ErrorInternal),
		string(ErrorIneligibleCommand), string(ErrorRejectedMissingIdentity),
		string(ErrorRejectedAmbiguousMetadata), string(ErrorRejectedOversized),
		string(ErrorRejectedQueueFull), string(ErrorRejectedHandoffFull),
		string(ErrorRejectedInvalidHash), string(ErrorRejectedUnsafeURL),
		string(ErrorRejectedUnknownField), string(ErrorRejectedInvalidSchema),
		string(ErrorRejectedPermission),
	}
	assertSameSet(t, "errorCategory", defEnum(t, schema, "errorCategory"), errorCategories)
	for _, category := range errorCategories {
		if !IsErrorCategory(ErrorCategory(category)) {
			t.Fatalf("IsErrorCategory(%q) = false", category)
		}
	}
	if IsErrorCategory("rejected_duplicate_terminal") {
		t.Fatal("rejected_duplicate_terminal must not be an error category")
	}
	if IsErrorCategory("bogus") {
		t.Fatal("IsErrorCategory(bogus) = true")
	}

	authStates := []string{
		string(AuthNotConnected), string(AuthAuthorizing), string(AuthConnected),
		string(AuthReauthorizationRequired), string(AuthTargetRepositoryUnavailable),
		string(AuthProtocolMismatch),
	}
	assertSameSet(t, "authState", defEnum(t, schema, "authState"), authStates)
	for _, state := range authStates {
		if !IsAuthState(AuthState(state)) {
			t.Fatalf("IsAuthState(%q) = false", state)
		}
	}
	if IsAuthState("bogus") {
		t.Fatal("IsAuthState(bogus) = true")
	}

	drainStates := []string{
		string(DrainIdle), string(DrainWorking), string(DrainWaitingForBackoff), string(DrainAuthorizing),
	}
	assertSameSet(t, "drainState", defEnum(t, schema, "drainState"), drainStates)
	for _, state := range drainStates {
		if !IsDrainState(DrainState(state)) {
			t.Fatalf("IsDrainState(%q) = false", state)
		}
	}
	if IsDrainState("bogus") {
		t.Fatal("IsDrainState(bogus) = true")
	}

	remoteFileKinds := []string{
		string(RemoteFile), string(RemoteDirectory), string(RemoteSymlink),
		string(RemoteSubmodule), string(RemoteMalformed), string(RemoteMissing),
	}
	assertSameSet(t, "remoteFileKind", defEnum(t, schema, "remoteFileKind"), remoteFileKinds)
	for _, kind := range remoteFileKinds {
		if !IsRemoteFileKind(RemoteFileKind(kind)) {
			t.Fatalf("IsRemoteFileKind(%q) = false", kind)
		}
	}
	if IsRemoteFileKind("bogus") {
		t.Fatal("IsRemoteFileKind(bogus) = true")
	}

	ackStatuses := []string{
		string(AckQueued), string(AckAlreadyQueued), string(AckRejectedInvalidSchema),
		string(AckRejectedUnknownField), string(AckRejectedOversized),
		string(AckRejectedInvalidHash), string(AckRejectedUnsafeURL),
		string(AckRejectedQueueFull), string(AckRejectedDuplicateTerminal),
	}
	for _, status := range ackStatuses {
		if !knownAckStatus(AckStatus(status)) {
			t.Fatalf("knownAckStatus(%q) = false", status)
		}
	}
	if knownAckStatus("bogus") {
		t.Fatal("knownAckStatus(bogus) = true")
	}
	submitRejections := []string{
		string(AckRejectedInvalidSchema), string(AckRejectedUnknownField),
		string(AckRejectedOversized), string(AckRejectedInvalidHash),
		string(AckRejectedUnsafeURL), string(AckRejectedQueueFull),
		string(AckRejectedDuplicateTerminal),
	}
	assertSameSet(t, "submitRejectionStatus", defEnum(t, schema, "submitRejectionStatus"), submitRejections)
}

func TestSchemaRequestAndResponseTypes(t *testing.T) {
	schema := readSchema(t, "native-messaging.schema.json")

	expectedTypes := []string{
		"connect", "submit_job", "status_request", "retry_job", "discard_job",
		"reset", "ack", "command_result", "status", "error",
	}
	entries, ok := schema["oneOf"].([]any)
	if !ok {
		t.Fatal("schema has no oneOf array")
	}
	var types []string
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("oneOf entry is not an object: %v", raw)
		}
		if entry["additionalProperties"] != false {
			t.Fatalf("oneOf entry %v must set additionalProperties=false", entry["title"])
		}
		properties, ok := entry["properties"].(map[string]any)
		if !ok {
			t.Fatalf("oneOf entry %v has no properties", entry["title"])
		}
		typeProperty, ok := properties["type"].(map[string]any)
		if !ok {
			t.Fatalf("oneOf entry %v has no type property", entry["title"])
		}
		constValue, ok := typeProperty["const"].(string)
		if !ok {
			t.Fatalf("oneOf entry %v type has no const", entry["title"])
		}
		types = append(types, constValue)
	}
	assertSameSet(t, "message types", types, expectedTypes)

	requiredByTitle := map[string][]string{
		"ConnectRequest":    {"type", "protocolVersion", "requestId", "extensionVersion"},
		"SubmitJobRequest":  {"type", "protocolVersion", "requestId", "job"},
		"StatusRequest":     {"type", "protocolVersion", "requestId"},
		"RetryJobRequest":   {"type", "protocolVersion", "requestId", "jobId"},
		"DiscardJobRequest": {"type", "protocolVersion", "requestId", "jobId", "confirmation"},
		"ResetRequest":      {"type", "protocolVersion", "requestId"},
		"SubmitAck":         ackResponseKeys,
		"CommandResult":     commandResultResponseKeys,
		"Status":            statusResponseKeys,
		"Error":             errorResponseKeys,
	}
	for title, expected := range requiredByTitle {
		entry := oneOfEntry(t, schema, title)
		rawRequired, ok := entry["required"].([]any)
		if !ok {
			t.Fatalf("%s has no required array", title)
		}
		required := make([]string, 0, len(rawRequired))
		for _, value := range rawRequired {
			required = append(required, value.(string))
		}
		assertSameSet(t, title+" required", required, expected)
	}

	authorization := defEntry(t, schema, "authorization")
	rawAuthRequired := authorization["required"].([]any)
	authRequired := make([]string, 0, len(rawAuthRequired))
	for _, value := range rawAuthRequired {
		authRequired = append(authRequired, value.(string))
	}
	assertSameSet(t, "authorization required", authRequired, authorizationKeys)

	counts := defEntry(t, schema, "counts")
	rawCountsRequired := counts["required"].([]any)
	countsRequired := make([]string, 0, len(rawCountsRequired))
	for _, value := range rawCountsRequired {
		countsRequired = append(countsRequired, value.(string))
	}
	assertSameSet(t, "counts required", countsRequired, countsKeys)

	jobSummary := defEntry(t, schema, "jobSummary")
	rawSummaryRequired := jobSummary["required"].([]any)
	summaryRequired := make([]string, 0, len(rawSummaryRequired))
	for _, value := range rawSummaryRequired {
		summaryRequired = append(summaryRequired, value.(string))
	}
	assertSameSet(t, "jobSummary required", summaryRequired, jobSummaryKeys)
}

func TestTranscriptJobSchemaMatchesGoStruct(t *testing.T) {
	schema := readSchema(t, "transcript-job.schema.json")
	if schema["additionalProperties"] != false {
		t.Fatal("transcript-job schema must set additionalProperties=false")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("transcript-job schema has no properties")
	}
	assertSameSet(t, "transcript job properties", objectKeys(t, properties), transcriptJobKeys)

	rawRequired, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("transcript-job schema has no required array")
	}
	required := make([]string, 0, len(rawRequired))
	for _, value := range rawRequired {
		required = append(required, value.(string))
	}
	assertSameSet(t, "transcript job required", required, transcriptJobKeys)

	serialized, err := validTestJob().MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(serialized, &decoded); err != nil {
		t.Fatalf("serialized job is not valid JSON: %v", err)
	}
	assertSameSet(t, "serialized job keys", objectKeys(t, decoded), transcriptJobKeys)
}

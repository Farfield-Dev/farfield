package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Farfield-Dev/farfield/history"
	"github.com/Farfield-Dev/farfield/internal/buildinfo"
	"github.com/Farfield-Dev/farfield/internal/storeopen"
	farfieldruntime "github.com/Farfield-Dev/farfield/runtime"
	farfieldserver "github.com/Farfield-Dev/farfield/server"
)

func Run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		usage(stderr)
		return 2
	}
	switch arguments[0] {
	case "version", "--version", "-version":
		fmt.Fprintln(stdout, buildinfo.Version)
		return 0
	case "history":
		return runHistory(arguments[1:], stdout, stderr)
	case "runtime":
		return runRuntime(arguments[1:], stdout, stderr)
	case "serve":
		return runServer(arguments[1:], stdout, stderr)
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", arguments[0])
		usage(stderr)
		return 2
	}
}

func runRuntime(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		runtimeUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "create":
		return createRun(arguments[1:], stdout, stderr)
	case "get":
		return getRun(arguments[1:], stdout, stderr)
	case "events":
		return runEvents(arguments[1:], stdout, stderr)
	case "transition":
		return transitionRun(arguments[1:], stdout, stderr)
	case "checkpoint":
		return saveRunCheckpoint(arguments[1:], stdout, stderr)
	case "verify":
		return verifyRuntime(arguments[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown runtime command %q\n\n", arguments[0])
		runtimeUsage(stderr)
		return 2
	}
}

func createRun(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("runtime create", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	runID := set.String("id", "", "stable run ID; generated when omitted")
	operationID := set.String("operation", "", "stable operation ID for idempotent retries")
	checkpoint := set.String("checkpoint", "", "optional JSON checkpoint")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *operationID == "" {
		fmt.Fprintln(stderr, "--operation is required")
		return 2
	}
	journal, err := openRuntime(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	event, err := journal.Create(context.Background(), farfieldruntime.CreateInput{
		RunID: *runID, OperationID: *operationID, Checkpoint: optionalJSON(*checkpoint),
	})
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, event)
}

func getRun(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("runtime get", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	runID := set.String("run", "", "run ID")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *runID == "" {
		fmt.Fprintln(stderr, "--run is required")
		return 2
	}
	journal, err := openRuntime(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	run, err := journal.Get(context.Background(), *runID)
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, run)
}

func runEvents(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("runtime events", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	runID := set.String("run", "", "run ID")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *runID == "" {
		fmt.Fprintln(stderr, "--run is required")
		return 2
	}
	journal, err := openRuntime(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	events, err := journal.Events(context.Background(), *runID)
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, events)
}

func transitionRun(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("runtime transition", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	runID := set.String("run", "", "run ID")
	operationID := set.String("operation", "", "stable operation ID for idempotent retries")
	to := set.String("to", "", "target status")
	checkpoint := set.String("checkpoint", "", "optional JSON checkpoint")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *runID == "" || *operationID == "" || *to == "" {
		fmt.Fprintln(stderr, "--run, --operation, and --to are required")
		return 2
	}
	journal, err := openRuntime(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	event, err := journal.Transition(context.Background(), farfieldruntime.TransitionInput{
		RunID: *runID, OperationID: *operationID, To: farfieldruntime.Status(*to), Checkpoint: optionalJSON(*checkpoint),
	})
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, event)
}

func saveRunCheckpoint(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("runtime checkpoint", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	runID := set.String("run", "", "run ID")
	operationID := set.String("operation", "", "stable operation ID for idempotent retries")
	checkpoint := set.String("checkpoint", "", "JSON checkpoint")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *runID == "" || *operationID == "" || *checkpoint == "" {
		fmt.Fprintln(stderr, "--run, --operation, and --checkpoint are required")
		return 2
	}
	journal, err := openRuntime(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	event, err := journal.SaveCheckpoint(context.Background(), farfieldruntime.CheckpointInput{
		RunID: *runID, OperationID: *operationID, Checkpoint: []byte(*checkpoint),
	})
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, event)
}

func verifyRuntime(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("runtime verify", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	journal, err := openRuntime(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	result, err := journal.Verify(context.Background())
	if err != nil {
		return printError(stderr, err)
	}
	if code := printJSON(stdout, result); code != 0 {
		return code
	}
	if !result.OK {
		return 1
	}
	return 0
}

func optionalJSON(value string) []byte {
	if value == "" {
		return nil
	}
	return []byte(value)
}

func runHistory(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		historyUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "append":
		return appendRecord(arguments[1:], stdout, stderr)
	case "append-batch":
		return appendBatch(arguments[1:], stdout, stderr)
	case "get":
		return getRecord(arguments[1:], stdout, stderr)
	case "list":
		return listRecords(arguments[1:], stdout, stderr)
	case "query":
		return queryRecords(arguments[1:], stdout, stderr)
	case "conversations":
		return conversations(arguments[1:], stdout, stderr)
	case "timeline":
		return timeline(arguments[1:], stdout, stderr)
	case "verify":
		return verify(arguments[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown history command %q\n\n", arguments[0])
		historyUsage(stderr)
		return 2
	}
}

type batchFile struct {
	ID      string            `json:"id"`
	Records []batchFileRecord `json:"records"`
}

type batchFileRecord struct {
	ID             string            `json:"id"`
	ConversationID string            `json:"conversation_id"`
	Kind           string            `json:"kind"`
	Content        json.RawMessage   `json:"content"`
	OccurredAt     *time.Time        `json:"occurred_at"`
	Sequence       *uint64           `json:"sequence"`
	TraceID        *string           `json:"trace_id"`
	SpanID         *string           `json:"span_id"`
	ParentID       *string           `json:"parent_id"`
	Agent          *string           `json:"agent"`
	Tool           *string           `json:"tool"`
	Status         *string           `json:"status"`
	Tags           map[string]string `json:"tags"`
}

func appendBatch(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history append-batch", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	file := set.String("file", "", "JSON batch file")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(stderr, "--file is required")
		return 2
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return printError(stderr, fmt.Errorf("read batch file: %w", err))
	}
	if len(data) > history.DefaultMaxSegmentBytes+1024*1024 {
		return printError(stderr, fmt.Errorf("batch file exceeds the request limit"))
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var value batchFile
	if err := decoder.Decode(&value); err != nil {
		return printError(stderr, fmt.Errorf("decode batch file: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return printError(stderr, fmt.Errorf("batch file must contain exactly one JSON object"))
	}
	inputs := make([]history.AppendInput, 0, len(value.Records))
	for index, record := range value.Records {
		if record.Content == nil {
			return printError(stderr, fmt.Errorf("records[%d].content is required; use null for an empty payload", index))
		}
		inputs = append(inputs, history.AppendInput{
			RecordID: record.ID, ConversationID: record.ConversationID,
			Kind: record.Kind, Content: record.Content, OccurredAt: record.OccurredAt,
			Sequence: record.Sequence, TraceID: record.TraceID, SpanID: record.SpanID,
			ParentID: record.ParentID, Agent: record.Agent, Tool: record.Tool,
			Status: record.Status, Tags: record.Tags,
		})
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	segment, err := service.AppendBatch(context.Background(), history.AppendBatchInput{SegmentID: value.ID, Records: inputs})
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, segment)
}

func queryRecords(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history query", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	conversation := set.String("conversation", "", "conversation ID")
	trace := set.String("trace", "", "trace ID")
	kind := set.String("kind", "", "record kind")
	agent := set.String("agent", "", "agent name")
	tool := set.String("tool", "", "tool name")
	status := set.String("status", "", "record status")
	limit := set.Int("limit", 100, "maximum records")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *limit < 1 || *limit > 1000 {
		fmt.Fprintln(stderr, "--limit must be between 1 and 1000")
		return 2
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	records, err := service.Query(context.Background(), history.Query{
		ConversationID: *conversation, TraceID: *trace, Kind: *kind,
		Agent: *agent, Tool: *tool, Status: *status, Limit: *limit,
	})
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, records)
}

func conversations(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history conversations", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	limit := set.Int("limit", 100, "maximum conversations")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *limit < 1 || *limit > 1000 {
		fmt.Fprintln(stderr, "--limit must be between 1 and 1000")
		return 2
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	values, err := service.Conversations(context.Background(), *limit)
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, values)
}

func timeline(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history timeline", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	conversation := set.String("conversation", "", "conversation ID")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *conversation == "" {
		fmt.Fprintln(stderr, "--conversation is required")
		return 2
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	entries, err := service.Timeline(context.Background(), *conversation)
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, entries)
}

func runServer(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	listen := set.String("listen", "127.0.0.1:8787", "HTTP listen address")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if strings.TrimSpace(*listen) == "" {
		fmt.Fprintln(stderr, "--listen cannot be empty")
		return 2
	}
	store, err := storeopen.Open(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	service, err := history.New(store)
	if err != nil {
		return printError(stderr, err)
	}
	journal, err := farfieldruntime.NewJournal(store)
	if err != nil {
		return printError(stderr, err)
	}
	httpServer, err := farfieldserver.New(service, farfieldserver.WithRuntime(journal))
	if err != nil {
		return printError(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	address := *listen
	if strings.HasPrefix(address, ":") {
		address = "127.0.0.1" + address
	}
	fmt.Fprintf(stdout, "Farfield listening on http://%s\n", address)
	if err := farfieldserver.ListenAndServe(ctx, *listen, httpServer.Handler()); err != nil {
		return printError(stderr, err)
	}
	return 0
}

func appendRecord(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history append", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	recordID := set.String("id", "", "stable record ID for idempotent retries")
	conversation := set.String("conversation", "", "conversation ID")
	kind := set.String("kind", "", "record kind")
	content := set.String("content", "null", "JSON content")
	occurred := set.String("occurred-at", "", "RFC3339 event time")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *conversation == "" || *kind == "" {
		fmt.Fprintln(stderr, "--conversation and --kind are required")
		return 2
	}
	var occurredAt *time.Time
	if *occurred != "" {
		value, err := time.Parse(time.RFC3339Nano, *occurred)
		if err != nil {
			fmt.Fprintf(stderr, "invalid --occurred-at: %v\n", err)
			return 2
		}
		occurredAt = &value
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	record, err := service.Append(context.Background(), history.AppendInput{
		RecordID: *recordID, ConversationID: *conversation, Kind: *kind,
		Content: []byte(*content), OccurredAt: occurredAt,
	})
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, record)
}

func getRecord(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history get", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	recordID := set.String("id", "", "record ID")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	if *recordID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	record, err := service.ReadRecord(context.Background(), *recordID)
	if err != nil {
		return printError(stderr, err)
	}
	content, err := service.ReadContent(context.Background(), record)
	if err != nil {
		return printError(stderr, err)
	}
	var decoded any
	if err := json.Unmarshal(content, &decoded); err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, map[string]any{"record": record, "content": decoded})
}

func listRecords(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history list", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	records, err := service.ListRecords(context.Background())
	if err != nil {
		return printError(stderr, err)
	}
	return printJSON(stdout, records)
}

func verify(arguments []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("history verify", flag.ContinueOnError)
	set.SetOutput(stderr)
	storeURI := set.String("store", ".farfield/objects", "local path, file://, s3://, or gs:// URI")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	service, err := openHistory(context.Background(), *storeURI)
	if err != nil {
		return printError(stderr, err)
	}
	result, err := service.Verify(context.Background())
	if err != nil {
		return printError(stderr, err)
	}
	if code := printJSON(stdout, result); code != 0 {
		return code
	}
	if !result.OK {
		return 1
	}
	return 0
}

func openHistory(ctx context.Context, uri string) (*history.Service, error) {
	store, err := storeopen.Open(ctx, uri)
	if err != nil {
		return nil, err
	}
	return history.New(store)
}

func openRuntime(ctx context.Context, uri string) (*farfieldruntime.Journal, error) {
	store, err := storeopen.Open(ctx, uri)
	if err != nil {
		return nil, err
	}
	return farfieldruntime.NewJournal(store)
}

func printJSON(writer io.Writer, value any) int {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(writer, "encode output: %v\n", err)
		return 1
	}
	return 0
}

func printError(stderr io.Writer, err error) int {
	var runtimeError *farfieldruntime.Error
	if errors.As(err, &runtimeError) {
		fmt.Fprintf(stderr, "%s: %s\n", runtimeError.Code, runtimeError.Message)
		return 1
	}
	var domainError *history.Error
	if errors.As(err, &domainError) {
		fmt.Fprintf(stderr, "%s: %s\n", domainError.Code, domainError.Message)
	} else {
		fmt.Fprintln(stderr, err)
	}
	return 1
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`Farfield — durable history and execution for agents

Usage:
  farfield history <command>
	farfield runtime <command>
  farfield serve [--store URI] [--listen ADDRESS]
  farfield version

Run "farfield history" or "farfield runtime" for command help.`))
}

func historyUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`Usage:
  farfield history append --conversation ID --kind KIND --content JSON [--store URI] [--id ID]
  farfield history append-batch --file FILE [--store URI]
  farfield history get --id ID [--store URI]
  farfield history list [--store URI]
  farfield history query [--conversation ID] [--kind KIND] [--limit N] [--store URI]
  farfield history conversations [--limit N] [--store URI]
  farfield history timeline --conversation ID [--store URI]
  farfield history verify [--store URI]`))
}

func runtimeUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`Usage:
  farfield runtime create --operation ID [--id ID] [--checkpoint JSON] [--store URI]
  farfield runtime get --run ID [--store URI]
  farfield runtime events --run ID [--store URI]
  farfield runtime transition --run ID --operation ID --to STATUS [--checkpoint JSON] [--store URI]
  farfield runtime checkpoint --run ID --operation ID --checkpoint JSON [--store URI]
  farfield runtime verify [--store URI]`))
}

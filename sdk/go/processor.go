package farfield

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type OverflowPolicy string

const (
	OverflowDrop  OverflowPolicy = "drop"
	OverflowBlock OverflowPolicy = "block"
)

type ProcessorOptions struct {
	MaxQueueSize  int
	MaxBatchSize  int
	ScheduleDelay time.Duration
	Overflow      OverflowPolicy
	OnError       func(error)
}

type ProcessorStats struct {
	Enqueued  uint64
	Committed uint64
	Dropped   uint64
	Failed    uint64
	Batches   uint64
	Pending   uint64
	LastError string
}

// BackgroundProcessor provides bounded, opt-in batching for high-volume
// capture. Submit acknowledges queue admission, not durable storage. Flush is
// the delivery boundary; direct Client writes remain durable acknowledgments.
type BackgroundProcessor struct {
	client    *Client
	batchSize int
	delay     time.Duration
	overflow  OverflowPolicy
	onError   func(error)
	queue     chan CaptureInput
	done      chan struct{}
	updates   chan struct{}

	mu     sync.Mutex
	closed bool
	stats  ProcessorStats
}

func NewBackgroundProcessor(client *Client, options ProcessorOptions) (*BackgroundProcessor, error) {
	if client == nil {
		return nil, errors.New("farfield: background processor requires a client")
	}
	if options.MaxQueueSize == 0 {
		options.MaxQueueSize = 8192
	}
	if options.MaxBatchSize == 0 {
		options.MaxBatchSize = 128
	}
	if options.ScheduleDelay == 0 {
		options.ScheduleDelay = 250 * time.Millisecond
	}
	if options.Overflow == "" {
		options.Overflow = OverflowDrop
	}
	if options.MaxQueueSize < 1 || options.MaxBatchSize < 1 || options.ScheduleDelay < 0 || options.Overflow != OverflowDrop && options.Overflow != OverflowBlock {
		return nil, errors.New("farfield: invalid background processor options")
	}
	processor := &BackgroundProcessor{
		client: client, batchSize: options.MaxBatchSize, delay: options.ScheduleDelay,
		overflow: options.Overflow, onError: options.OnError,
		queue: make(chan CaptureInput, options.MaxQueueSize), done: make(chan struct{}), updates: make(chan struct{}, 1),
	}
	go processor.run()
	return processor, nil
}

// Submit snapshots context-local metadata and admits a capture to the queue.
// False with a nil error means the configured drop policy rejected a full queue.
func (processor *BackgroundProcessor) Submit(ctx context.Context, input CaptureInput) (bool, error) {
	prepared, err := processor.client.PrepareCapture(ctx, input)
	if errors.Is(err, ErrDropped) {
		processor.mu.Lock()
		processor.stats.Dropped++
		processor.mu.Unlock()
		return false, nil
	}
	if err != nil {
		return false, err
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	if processor.closed {
		return false, errors.New("farfield: background processor is shut down")
	}
	if processor.overflow == OverflowDrop {
		select {
		case processor.queue <- prepared:
			processor.stats.Enqueued++
			processor.stats.Pending++
			return true, nil
		default:
			processor.stats.Dropped++
			return false, nil
		}
	}
	select {
	case processor.queue <- prepared:
		processor.stats.Enqueued++
		processor.stats.Pending++
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Flush waits until captures admitted before the call have completed delivery.
func (processor *BackgroundProcessor) Flush(ctx context.Context) error {
	processor.mu.Lock()
	target := processor.stats.Enqueued
	processor.mu.Unlock()
	for {
		processor.mu.Lock()
		finished := processor.stats.Committed + processor.stats.Failed
		failures := processor.stats.Failed
		lastError := processor.stats.LastError
		processor.mu.Unlock()
		if finished >= target {
			if failures != 0 {
				return fmt.Errorf("farfield: background delivery failed: %s", lastError)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-processor.updates:
		}
	}
}

// Shutdown stops admission, drains the queue, and waits for the worker.
func (processor *BackgroundProcessor) Shutdown(ctx context.Context) error {
	processor.mu.Lock()
	if !processor.closed {
		processor.closed = true
		close(processor.queue)
	}
	processor.mu.Unlock()
	flushErr := processor.Flush(ctx)
	select {
	case <-processor.done:
		return flushErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (processor *BackgroundProcessor) Stats() ProcessorStats {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	return processor.stats
}

func (processor *BackgroundProcessor) run() {
	defer close(processor.done)
	for first := range processor.queue {
		batch := []CaptureInput{first}
		timer := time.NewTimer(processor.delay)
	collect:
		for len(batch) < processor.batchSize {
			select {
			case value, ok := <-processor.queue:
				if !ok {
					if !timer.Stop() {
						<-timer.C
					}
					processor.deliver(batch)
					return
				}
				batch = append(batch, value)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		processor.deliver(batch)
	}
}

func (processor *BackgroundProcessor) deliver(batch []CaptureInput) {
	groups := map[string][]CaptureInput{}
	for _, input := range batch {
		groups[input.ConversationID] = append(groups[input.ConversationID], input)
	}
	for _, records := range groups {
		_, err := processor.client.CapturePreparedBatch(context.Background(), BatchInput{Records: records})
		processor.mu.Lock()
		if err != nil {
			processor.stats.Failed += uint64(len(records))
			processor.stats.LastError = err.Error()
		} else {
			processor.stats.Committed += uint64(len(records))
			processor.stats.Batches++
		}
		processor.stats.Pending -= uint64(len(records))
		processor.mu.Unlock()
		if err != nil && processor.onError != nil {
			callOnError(processor.onError, err)
		}
		select {
		case processor.updates <- struct{}{}:
		default:
		}
	}
}

func callOnError(callback func(error), err error) {
	defer func() {
		_ = recover()
	}()
	callback(err)
}

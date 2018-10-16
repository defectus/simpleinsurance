package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"
)

// WindowSizeInSeconds defines the period over which we sum no. invocations.
const WindowSizeInSeconds = 60

// SaveFilename is name of file we use to persist invocations.
const SaveFilename = "invocations.txt"

// ElementSeparator defines the invocation file element separator.
const ElementSeparator = "\n"

// InvocationRecorder stores invocation and maintain access to them.
type InvocationRecorder struct {
	mutex           *sync.Mutex
	invocationTimes []time.Time
	prunedEvents    chan []time.Time
	debug           bool
}

// NewInvocationRecorder creates instance of Invocationrecorder.
func NewInvocationRecorder(prunedEvents chan []time.Time, debug bool) *InvocationRecorder {
	return &InvocationRecorder{
		mutex:           &sync.Mutex{},
		invocationTimes: make([]time.Time, 100),
		debug:           debug,
		prunedEvents:    prunedEvents,
	}
}

// Record stores invocaion and returns number of recent invocations ()
func (i *InvocationRecorder) Record(when time.Time) int {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	now := time.Now()
	start := now.Add(-1 * WindowSizeInSeconds * time.Second)
	i.invocationTimes = append(i.invocationTimes, now)
	count := 0
	for _, t := range i.invocationTimes {
		if (t.After(start) || t.Equal(start)) && (t.Before(now) || t.Equal(now)) {
			count++
		}
	}
	return count
}

// Prune eliminates useless events (older than time window) and let the remaining of events persist.
func (i *InvocationRecorder) Prune() {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.debug {
		log.Printf("pruning events before: %d", len(i.invocationTimes))
	}
	invocationsToSave := []time.Time{}
	cutTime := time.Now().Add(-1 * WindowSizeInSeconds * time.Second)
	for _, t := range i.invocationTimes {
		if t.After(cutTime) || t.Equal(cutTime) {
			invocationsToSave = append(invocationsToSave, t)
		}
	}
	if i.debug {
		log.Printf("pruning events after: %d", len(invocationsToSave))
	}
	i.invocationTimes = invocationsToSave
	i.prunedEvents <- invocationsToSave
}

// Load reads saved invocaion from file into its internal storage.
func (i *InvocationRecorder) Load(filename string) {
	bytes, _ := ioutil.ReadFile(filename)
	elements := strings.Split(string(bytes), ElementSeparator)
	savedInvocations := make([]time.Time, len(elements))
	for _, elem := range elements {
		if len(elem) == 0 {
			continue
		}
		parsedTime := &time.Time{}
		err := parsedTime.UnmarshalText([]byte(elem))
		if err != nil {
			log.Printf("error parsing binary time element %s", elem)
			continue
		}
		savedInvocations = append(savedInvocations, *parsedTime)
	}
	json.Unmarshal(bytes, &savedInvocations)
	i.invocationTimes = savedInvocations
}

func createHandler(invocationRecorder *InvocationRecorder, events chan time.Time) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		events <- now
		count := invocationRecorder.Record(now)
		w.Write([]byte(fmt.Sprintf("%d", count)))
	})
}

func hookOnExit(cancel context.CancelFunc) {
	go func(cancel context.CancelFunc) {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Kill, os.Interrupt)
		<-signals
		log.Println("hookOnExit: initiating shutdown ...")
		cancel()
	}(cancel)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	ctx, cancel := context.WithCancel(context.Background())
	hookOnExit(cancel)

	debug := false
	flag.BoolVar(&debug, "d", false, "show debug information")
	flag.Parse()

	events := make(chan time.Time, 100)
	prunedEvents := make(chan []time.Time, 10)

	invocations := NewInvocationRecorder(prunedEvents, debug)
	invocations.Load(SaveFilename)

	go func() {
		f, err := os.OpenFile(SaveFilename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Panicf("failed to open save file %+v", err)
		}
		for {
			select {
			case <-ctx.Done():
				f.Close()
				return
			case event := <-events:
				stringEvent, err := event.MarshalText()
				if err != nil {
					log.Printf("failed to serialize event time %v", event)
					continue
				}
				f.WriteString(string(stringEvent) + ElementSeparator)
			case events := <-prunedEvents:
				f.Truncate(0)
				f.Seek(0, 0)
				for _, event := range events {
					stringEvent, err := event.MarshalText()
					if err != nil {
						log.Printf("failed to serialize event time %v", event)
						continue
					}
					f.WriteString(string(stringEvent) + ElementSeparator)
				}
			}
		}
	}()
	// every minute clean up the file so it doesn't grow too big
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				invocations.Prune()
			}
		}
	}()
	// wait for requests
	go http.ListenAndServe("0.0.0.0:8081", createHandler(invocations, events))
	<-ctx.Done()
	close(events)
}

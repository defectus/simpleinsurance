package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSimpleCounting(t *testing.T) {
	invocationRecorder := NewInvocationRecorder(false)
	events := make(chan time.Time, 100)
	ts := httptest.NewServer(createHandler(invocationRecorder, events))
	defer ts.Close()
	res, err := http.Get(ts.URL)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != http.StatusOK {
		log.Printf("unexpected status %d", res.StatusCode)
		t.Fail()
	}
	p, _ := ioutil.ReadAll(res.Body)
	if string(p) != "1" {
		log.Printf("expected to get 1, got %s", string(p))
	}
}

func TestMultiCounting(t *testing.T) {
	invocationRecorder := NewInvocationRecorder(false)
	events := make(chan time.Time, 100)
	ts := httptest.NewServer(createHandler(invocationRecorder, events))
	defer ts.Close()
	counts := make([]string, 100, 100)
	wg := &sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := http.Get(ts.URL)
			if err != nil {
				t.Error(err)
			}
			if res.StatusCode != http.StatusOK {
				log.Printf("unexpected status %d", res.StatusCode)
				t.Fail()
			}
			p, _ := ioutil.ReadAll(res.Body)
			counts[i] = string(p)
		}(i)
	}
	wg.Wait()
	for i := 1; i <= 100; i++ {
		found := 0
		for _, count := range counts {
			str, _ := strconv.Atoi(count)
			if str == i {
				found++
			}
		}
		if found == 0 {
			t.Errorf("not found %d", i)
		}
		if found > 1 {
			t.Errorf("found too many %d (%d)", i, found)
		}
	}
}

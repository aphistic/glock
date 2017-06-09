package glock

import (
	"sort"
	"sync"
	"time"
)

type mockTriggers []*mockTrigger

func (mt mockTriggers) Len() int {
	return len(mt)
}
func (mt mockTriggers) Less(i, j int) bool {
	return mt[i].trigger.Before(mt[j].trigger)
}
func (mt mockTriggers) Swap(i, j int) {
	mt[i], mt[j] = mt[j], mt[i]
}

type mockTrigger struct {
	trigger time.Time
	ch      chan time.Time
}

// MockClock is an implementation of Clock that can be moved forward in time
// in increments for testing code that relies on timeouts or other time-sensitive
// constructs.
type MockClock struct {
	fakeTime time.Time

	afterLock sync.Mutex
	triggers  mockTriggers
	afterArgs []time.Duration

	tickerLock sync.Mutex
	tickers    []*mockTicker
	tickerArgs []time.Duration
}

// NewMockClock creates a new instance of MockClock with the internal time set
// to time.Now()
func NewMockClock() *MockClock {
	return &MockClock{
		fakeTime: time.Now(),

		tickers: make([]*mockTicker, 0),

		afterArgs:  make([]time.Duration, 0),
		tickerArgs: make([]time.Duration, 0),
	}
}

func (mc *MockClock) processTickers() {
	mc.tickerLock.Lock()
	defer mc.tickerLock.Unlock()

	now := mc.Now()
	for _, ticker := range mc.tickers {
		ticker.process(now)
	}
}

func (mc *MockClock) processTriggers() {
	mc.afterLock.Lock()
	mc.afterLock.Unlock()

	now := mc.Now()
	triggered := 0
	for _, trigger := range mc.triggers {
		if trigger.trigger.Before(now) || trigger.trigger.Equal(now) {
			trigger.ch <- trigger.trigger
			triggered++
		}
	}

	mc.triggers = mc.triggers[triggered:]
}

// SetCurrent sets the internal MockClock time to the supplied time.
func (mc *MockClock) SetCurrent(current time.Time) {
	mc.fakeTime = current
}

// Advance will advance the internal MockClock time by the supplied time.
func (mc *MockClock) Advance(duration time.Duration) {
	mc.fakeTime = mc.fakeTime.Add(duration)
	mc.processTickers()
	mc.processTriggers()
}

// Now returns the current time internal to the MockClock
func (mc *MockClock) Now() time.Time {
	return mc.fakeTime
}

// After returns a channel that will be sent the current internal MockClock
// time once the MockClock's internal time is at or past the provided duration
func (mc *MockClock) After(duration time.Duration) <-chan time.Time {
	mc.afterLock.Lock()
	defer mc.afterLock.Unlock()

	trigger := &mockTrigger{
		trigger: mc.fakeTime.Add(duration),
		ch:      make(chan time.Time, 1),
	}
	mc.triggers = append(mc.triggers, trigger)
	sort.Sort(mc.triggers)

	mc.afterArgs = append(mc.afterArgs, duration)

	return trigger.ch
}

// Sleep will block until the internal MockClock time is at or past the
// provided duration
func (mc *MockClock) Sleep(duration time.Duration) {
	<-mc.After(duration)
}

// GetAfterArgs returns the duration of each call to After in the
// same order as they were called. The list is cleared each time
// GetAfterArgs is called.
func (mc *MockClock) GetAfterArgs() []time.Duration {
	mc.afterLock.Lock()
	defer mc.afterLock.Unlock()

	args := mc.afterArgs
	mc.afterArgs = mc.afterArgs[:0]
	return args
}

// GetTickerArgs returns the duration of each call to create a new
// ticker in the same order as they were called. The list is cleared
// each time GetTickerArgs is called.
func (mc *MockClock) GetTickerArgs() []time.Duration {
	mc.tickerLock.Lock()
	defer mc.tickerLock.Unlock()

	args := mc.tickerArgs
	mc.tickerArgs = mc.tickerArgs[:0]
	return args
}

type mockTicker struct {
	clock    *MockClock
	duration time.Duration

	started  time.Time
	nextTick time.Time

	processLock  sync.Mutex
	processQueue []time.Time

	writeLock sync.Mutex
	writing   bool
	ch        chan time.Time

	stopped bool
}

// NewTicker creates a new Ticker tied to the internal MockClock time that ticks
// at intervals similar to time.NewTicker().  It will also skip or drop ticks
// for slow readers similar to time.NewTicker() as well.
func (mc *MockClock) NewTicker(duration time.Duration) Ticker {
	if duration == 0 {
		panic("duration cannot be 0")
	}

	now := mc.Now()

	ft := &mockTicker{
		clock:    mc,
		duration: duration,

		started:  now,
		nextTick: now.Add(duration),

		processQueue: make([]time.Time, 0),
		ch:           make(chan time.Time),
	}

	mc.tickerLock.Lock()
	mc.tickers = append(mc.tickers, ft)
	mc.tickerArgs = append(mc.tickerArgs, duration)
	mc.tickerLock.Unlock()

	return ft
}

func (mt *mockTicker) process(now time.Time) {
	if mt.stopped {
		return
	}

	mt.processLock.Lock()
	mt.processQueue = append(mt.processQueue, now)
	mt.processLock.Unlock()

	if !mt.writing && (mt.nextTick.Before(now) || mt.nextTick.Equal(now)) {
		mt.writeLock.Lock()

		mt.writing = true
		go func() {
			defer mt.writeLock.Unlock()

			for {
				mt.processLock.Lock()
				if len(mt.processQueue) == 0 {
					mt.processLock.Unlock()
					break
				}

				procTime := mt.processQueue[0]
				mt.processQueue = mt.processQueue[1:]

				mt.processLock.Unlock()

				if mt.nextTick.After(procTime) {
					continue
				}

				mt.ch <- mt.nextTick

				durationMod := procTime.Sub(mt.started) % mt.duration

				if durationMod == 0 {
					mt.nextTick = procTime.Add(mt.duration)
				} else if procTime.Sub(mt.nextTick) > mt.duration {
					mt.nextTick = procTime.Add(mt.duration - durationMod)
				} else {
					mt.nextTick = mt.nextTick.Add(mt.duration)
				}
			}

			mt.writing = false
		}()
	}
}

// Chan returns a channel which will receive the MockClock's internal time
// at the interval given when creating the ticker.
func (mt *mockTicker) Chan() <-chan time.Time {
	return mt.ch
}

// Stop will stop the ticker from ticking
func (mt *mockTicker) Stop() {
	mt.stopped = true
}

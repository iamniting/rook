/*
Copyright 2021 The Rook Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package k8sutil for Kubernetes helpers.
package k8sutil

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

type eventObject struct {
	eventsReportedAt time.Time
	eventCount       int
}

// EventReporter is custom events reporter type which allows user to limit the events
type EventReporter struct {
	recorder       record.EventRecorder
	reportedEvents map[string]*eventObject

	// report events x times where x is count
	count int

	// report events after x minutes
	eventReportAfterMinutes int

	// lastReportedEvent will have a last captured event
	lastReportedEvent string

	// lastReportedEventTime will be the time of lastReportedEvent
	lastReportedEventTime time.Time
}

// NewEventReporter returns EventReporter object
func NewEventReporter(recorder record.EventRecorder, maxCountInGivenTime, reportAfter int) *EventReporter {
	er := &EventReporter{
		recorder:                recorder,
		count:                   maxCountInGivenTime,
		eventReportAfterMinutes: reportAfter,
	}

	er.reportedEvents = map[string]*eventObject{}

	return er
}

// Report records a events if eventReportAfterMinutes has passed or events occurred less than count
func (rep *EventReporter) Report(instance runtime.Object, eventType, eventReason, msg string) {

	eventKey, err := getEventKey(instance, eventType, eventReason, msg)
	if err != nil {
		return
	}

	eventobj, ok := rep.reportedEvents[eventKey]
	if !ok {
		// create a event object for the first occurrence
		logger.Info("Reporting Event ", eventKey)
		eventobj = &eventObject{eventsReportedAt: time.Now(), eventCount: 1}
		rep.reportedEvents[eventKey] = eventobj
		rep.recorder.Event(instance, eventType, eventReason, msg)
		rep.lastReportedEvent = eventKey
		rep.lastReportedEventTime = eventobj.eventsReportedAt
	} else if eventobj.eventsReportedAt.Add(time.Minute * time.Duration(rep.eventReportAfterMinutes)).Before(time.Now()) {
		// given time has elapsed, create events again and mark the counter as 1 to track them again within a given time period
		logger.Info("Reporting Event ", eventKey)
		eventobj.eventCount = 1
		eventobj.eventsReportedAt = time.Now()
		rep.recorder.Event(instance, eventType, eventReason, msg)
		rep.lastReportedEvent = eventKey
		rep.lastReportedEventTime = eventobj.eventsReportedAt
	} else if eventobj.eventCount < rep.count {
		// given time has not elapsed yet from the first occurrence, create an event as occurrence count is less than given count
		logger.Info("Reporting Event ", eventKey)
		eventobj.eventCount++
		rep.recorder.Event(instance, eventType, eventReason, msg)
		rep.lastReportedEvent = eventKey
		rep.lastReportedEventTime = time.Now()
	} else {
		logger.Debug("Not Reporting Event because event occurrence surpassed given count:",
			rep.count, " and time frame:", rep.eventReportAfterMinutes, " for Event:", eventKey)
	}
}

// ReportIfNotPresent will report event if lastReportedEvent is not the same in last 60 minutes
func (rep *EventReporter) ReportIfNotPresent(instance runtime.Object, eventType, eventReason, msg string) {

	eventKey, err := getEventKey(instance, eventType, eventReason, msg)
	if err != nil {
		return
	}

	if rep.lastReportedEvent != eventKey || rep.lastReportedEventTime.Add(time.Minute*60).Before(time.Now()) {
		logger.Info("Reporting Event ", eventKey)
		rep.lastReportedEvent = eventKey
		rep.lastReportedEventTime = time.Now()
		rep.recorder.Event(instance, eventType, eventReason, msg)
	} else {
		logger.Debug("Not Reporting Event because event is same as the old one:", eventKey)
	}
}

func getEventKey(instance runtime.Object, eventType, eventReason, msg string) (string, error) {

	objMeta, err := meta.Accessor(instance)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%s:%s:%s", objMeta.GetName(), eventType, eventReason, msg), nil
}

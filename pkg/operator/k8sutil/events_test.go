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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

func getEventsOccurences(channel chan string) map[string]int {

	foundEvents := make(map[string]int)

	for len(channel) > 0 {
		e := <-channel
		foundEvents[e]++
	}

	return foundEvents
}

func TestReport(t *testing.T) {

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}

	testCases := []struct {
		eventReprted  int
		eventOccurred int
		changeTime    int
	}{
		{
			eventReprted:  1,
			eventOccurred: 1,
			changeTime:    0,
		},
		{
			eventReprted:  5,
			eventOccurred: 5,
			changeTime:    0,
		},
		{
			eventReprted:  10,
			eventOccurred: 5,
			changeTime:    0,
		},
		{
			eventReprted:  1,
			eventOccurred: 2,
			changeTime:    1,
		},
		{
			eventReprted:  5,
			eventOccurred: 10,
			changeTime:    1,
		},
		{
			eventReprted:  10,
			eventOccurred: 10,
			changeTime:    1,
		},
		{
			eventReprted:  1,
			eventOccurred: 3,
			changeTime:    2,
		},
		{
			eventReprted:  5,
			eventOccurred: 15,
			changeTime:    2,
		},
		{
			eventReprted:  10,
			eventOccurred: 15,
			changeTime:    2,
		},
	}

	for _, tc := range testCases {
		eventType, eventReason, eventMsg := corev1.EventTypeNormal, "Created", "Pod has been created"

		frecorder := record.NewFakeRecorder(1024)
		reporter := NewEventReporter(frecorder, 5, 20)

		for i := 0; i < tc.eventReprted; i++ {
			reporter.Report(pod, eventType, eventReason, eventMsg)
		}

		for i := 0; i < tc.changeTime; i++ {
			ekey, err := getEventKey(pod, eventType, eventReason, eventMsg)
			assert.NoError(t, err)

			ftime := reporter.reportedEvents[ekey].eventsReportedAt.Add(time.Minute * -20)
			reporter.reportedEvents[ekey].eventsReportedAt = ftime

			for i := 0; i < tc.eventReprted; i++ {
				reporter.Report(pod, eventType, eventReason, eventMsg)
			}
		}

		foundEvents := getEventsOccurences(frecorder.Events)
		assert.Equal(t, tc.eventOccurred, foundEvents[eventType+" "+eventReason+" "+eventMsg])
	}
}

func TestReportIfNotPresent(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod1",
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod2",
		},
	}

	testCases := []struct {
		eventReprted       int
		changeTime         bool
		ReportAnotherEvent bool
	}{
		{
			eventReprted: 1,
		},
		{
			eventReprted: 2,
		},
		{
			eventReprted: 1,
			changeTime:   true,
		},
		{
			eventReprted: 2,
			changeTime:   true,
		},
		{
			eventReprted:       1,
			ReportAnotherEvent: true,
		},
		{
			eventReprted:       1,
			ReportAnotherEvent: true,
		},
	}

	for _, tc := range testCases {
		eventType, eventReason, eventMsg := corev1.EventTypeNormal, "Created", "Pod has been created"

		frecorder := record.NewFakeRecorder(1024)
		reporter := NewEventReporter(frecorder, 5, 20)

		for i := 0; i < tc.eventReprted; i++ {
			reporter.ReportIfNotPresent(pod1, eventType, eventReason, eventMsg)
		}

		foundEvents := getEventsOccurences(frecorder.Events)
		assert.Equal(t, 1, foundEvents[eventType+" "+eventReason+" "+eventMsg])

		if tc.changeTime {
			ftime := reporter.lastReportedEventTime.Add(time.Minute * -60)
			reporter.lastReportedEventTime = ftime

			reporter.ReportIfNotPresent(pod1, eventType, eventReason, eventMsg)
			foundEvents := getEventsOccurences(frecorder.Events)
			assert.Equal(t, 1, foundEvents[eventType+" "+eventReason+" "+eventMsg])
		}

		if tc.ReportAnotherEvent {
			eventType, eventReason, eventMsg := corev1.EventTypeNormal, "Created", "Pod is running"
			reporter.ReportIfNotPresent(pod2, eventType, eventReason, eventMsg)
			foundEvents := getEventsOccurences(frecorder.Events)
			assert.Equal(t, 1, foundEvents[eventType+" "+eventReason+" "+eventMsg])

			eventType, eventReason, eventMsg = corev1.EventTypeNormal, "Created", "Pod has been created"
			reporter.ReportIfNotPresent(pod1, eventType, eventReason, eventMsg)
			foundEvents = getEventsOccurences(frecorder.Events)
			assert.Equal(t, 1, foundEvents[eventType+" "+eventReason+" "+eventMsg])
		}

	}
}

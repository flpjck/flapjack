package main

import (
	"fmt"
	"log"
	"os"
	"io/ioutil"
	"time"
	"net/http"
	"flapjack"
	"encoding/json"
)

// handler caches
func handler(newState chan flapjack.Event, w http.ResponseWriter, r *http.Request) {
	var state flapjack.Event

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		message := "Error: Couldn't read request body: %s\n"
		log.Printf(message, err)
		fmt.Fprintf(w, message, err)
		return
	}

	err = json.Unmarshal(body, &state)
	if err != nil {
		message := "Error: Couldn't read request body: %s\n"
		log.Println(message, err)
		fmt.Fprintf(w, message, err)
		return
	}

	// Populate a time if none has been set.
	if state.Time == 0 {
		state.Time = time.Now().Unix()
	}

	if len(state.Type) == 0 {
		state.Type = "service"
	}

	newState <- state

	json, _ := json.Marshal(state)
	fmt.Printf("Caching event: %s\n", json)
	fmt.Fprintf(w, "Caching event: %s\n", json)
}

// cacheState stores a cache of event state to be sent to Flapjack.
// The event state is queried later when submitting events periodically
// to Flapjack.
func cacheState(newState chan flapjack.Event, state map[string]flapjack.Event) {
	for ns := range newState {
		key := ns.Entity + ":" + ns.Check
		state[key] = ns
	}
}

// submitCachedState periodically samples the cached state, sends it to Flapjack.
func submitCachedState(states map[string]flapjack.Event) {
	transport, err := flapjack.Dial("localhost:6379", 13)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}

	for {
		fmt.Printf("Number of cached states: %d\n", len(states))
		for id, state := range(states) {
			event := flapjack.Event{
				Entity:  state.Entity,
				Check:   state.Check,
				Type:    state.Type,
				State:   state.State,
				Summary: state.Summary,
				Time:    time.Now().Unix(),
			}

			fmt.Printf("%s : %+v\n", id, event)
			transport.Send(event)
		}
		time.Sleep(1 * time.Second)
	}
}

func main() {
	newState := make(chan flapjack.Event)
	state := map[string]flapjack.Event{}

	go cacheState(newState, state)
	go submitCachedState(state)

	http.HandleFunc("/", func (w http.ResponseWriter, r *http.Request) {
		handler(newState, w, r)
	})
	http.ListenAndServe(":8080", nil)
}
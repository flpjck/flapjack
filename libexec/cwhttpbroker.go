package main

import (
	"encoding/json"
	"flapjack"
	"fmt"
	"github.com/go-martini/martini"
	"gopkg.in/alecthomas/kingpin.v1"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

// State is a basic representation of a Flapjack event, with some extra field.
// The extra fields handle state expiry.
// Find more at http://flapjack.io/docs/1.0/development/DATA_STRUCTURES
type State struct {
	flapjack.Event
	TTL int64 `json:"ttl"`
}
type SNSNotification struct {
	Message          string `json:"Message"`
	MessageID        string `json:"MessageId"`
	Signature        string `json:"Signature"`
	SignatureVersion string `json:"SignatureVersion"`
	SigningCertURL   string `json:"SigningCertURL"`
	Subject          string `json:"Subject"`
	Timestamp        string `json:"Timestamp"`
	TopicArn         string `json:"TopicArn"`
	Type             string `json:"Type"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
}
type CWAlarm struct {
	AWSAccountID     string      `json:"AWSAccountId"`
	AlarmDescription interface{} `json:"AlarmDescription"`
	AlarmName        string      `json:"AlarmName"`
	NewStateReason   string      `json:"NewStateReason"`
	NewStateValue    string      `json:"NewStateValue"`
	OldStateValue    string      `json:"OldStateValue"`
	Region           string      `json:"Region"`
	StateChangeTime  string      `json:"StateChangeTime"`
	Trigger          struct {
		ComparisonOperator string `json:"ComparisonOperator"`
		Dimensions         []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"Dimensions"`
		EvaluationPeriods int         `json:"EvaluationPeriods"`
		MetricName        string      `json:"MetricName"`
		Namespace         string      `json:"Namespace"`
		Period            int         `json:"Period"`
		Statistic         string      `json:"Statistic"`
		Threshold         float64         `json:"Threshold"`
		Unit              interface{} `json:"Unit"`
	} `json:"Trigger"`
}

// handler caches
func CreateState(updates chan State, w http.ResponseWriter, r *http.Request) {
	var state State
    var snsmessage SNSNotification
    var cwalarmmessage CWAlarm
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		message := "Error: Couldn't read request body: %s\n"
		log.Printf(message, err)
		fmt.Fprintf(w, message, err)
		return
	}
    err = json.Unmarshal(body, &snsmessage)
    if err != nil {
		message := "Error: Couldn't read request body from the SNS notification: %s\n"
		log.Println(message, err)
		fmt.Fprintf(w, message, err)
		return
	}
        inputmessage := []byte(snsmessage.Message)
	err = json.Unmarshal(inputmessage, &cwalarmmessage)
    if err != nil {
		message := "Error: Couldn't read request body from the SNS message: %s\n"
		log.Println(message, err)
		fmt.Fprintf(w, message, err)
		return
	}
        alarmstate := "OK"
        if cwalarmmessage.NewStateValue == "ALARM" {

            alarmstate = "CRITICAL"
        }
	s := `{"entity": "` + cwalarmmessage.AlarmName + `", "check": "` + cwalarmmessage.Trigger.MetricName +`", "type": "service", "tags": "asg", "state": "` + alarmstate +	`", "summary": "` + cwalarmmessage.NewStateReason + `", "ttl": 30}`
        inputstate := []byte(s)
	err = json.Unmarshal(inputstate, &state)
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

	if state.TTL == 0 {
		state.TTL = 300
	}

	updates <- state

	json, _ := json.Marshal(state)
	message := "Caching state: %s\n"
	log.Printf(message, json)
	fmt.Fprintf(w, message, json)
}

// cacheState stores a cache of event state to be sent to Flapjack.
// The event state is queried later when submitting events periodically
// to Flapjack.
func cacheState(updates chan State, state map[string]State) {
	for ns := range updates {
		key := ns.Entity + ":" + ns.Check
		state[key] = ns
	}
}

// submitCachedState periodically samples the cached state, sends it to Flapjack.
func submitCachedState(states map[string]State, config Config) {
	transport, err := flapjack.Dial(config.Server, config.Database)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}

	for {
		log.Printf("Number of cached states: %d\n", len(states))
		for id, state := range states {
			now := time.Now().Unix()
			event := flapjack.Event{
				Entity:  state.Entity,
				Check:   state.Check,
				Type:    state.Type,
				State:   state.State,
				Summary: state.Summary,
				Time:    now,
			}

			if config.Debug {
				log.Printf("Sending event data for %s\n", id)
			}
			transport.Send(event)
		}
		time.Sleep(config.Interval)
	}
}

var (
	port     = kingpin.Flag("port", "Address to bind HTTP server (default 3090)").Default("80").OverrideDefaultFromEnvar("PORT").String()
	server   = kingpin.Flag("server", "Redis server to connect to (default localhost:6380)").Default("localhost:6380").String()
	database = kingpin.Flag("database", "Redis database to connect to (default 0)").Int() // .Default("13").Int()
	interval = kingpin.Flag("interval", "How often to submit events (default 10s)").Default("10s").Duration()
	debug    = kingpin.Flag("debug", "Enable verbose output (default false)").Bool()
)

type Config struct {
	Port     string
	Server   string
	Database int
	Interval time.Duration
	Debug    bool
}

func main() {
	kingpin.Version("0.0.1")
	kingpin.Parse()

	config := Config{
		Server:   *server,
		Database: *database,
		Interval: *interval,
		Debug:    *debug,
		Port:     ":" + *port,
	}
	if config.Debug {
		log.Printf("Booting with config: %+v\n", config)
	}

	updates := make(chan State)
	state := map[string]State{}

	go cacheState(updates, state)
	go submitCachedState(state, config)

	m := martini.Classic()
	m.Group("/state", func(r martini.Router) {
		r.Post("", func(res http.ResponseWriter, req *http.Request) {
			CreateState(updates, res, req)
		})
		r.Get("", func() []byte {
			data, _ := json.Marshal(state)
			return data
		})
	})

	log.Fatal(http.ListenAndServe(config.Port, m))
}
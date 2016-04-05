package console

import (
	"bufio"
	"log"
	"os"
	"strings"
	"time"

	"github.com/netflix/hal-9001/hal"
)

type Config struct{}

type Broker struct {
	User   string
	Room   string
	Stdin  chan string
	Stdout chan string
}

type SlashReaction string

func (c Config) NewBroker(name string) Broker {
	user := os.Getenv("USER")
	if user == "" {
		user = "testuser"
	}

	out := Broker{
		User:   user,
		Room:   name,
		Stdin:  make(chan string, 1000),
		Stdout: make(chan string, 1000),
	}

	return out
}

func (cb Broker) Name() string {
	return cb.Room
}

func (cb Broker) Send(e hal.Evt) {
	cb.Stdout <- e.Body
	//cb.Stdout <- fmt.Sprintf("%s/%s: %s\n", e.User, e.Room, e.Body)
}

func (cb Broker) SendTable(e hal.Evt, hdr []string, rows [][]string) {
	cb.Stdout <- hal.Utf8Table(hdr, rows)
}

// SimpleStdin will loop forever reading stdin and publish each line
// as an event in the console broker.
// e.g. go cbroker.SimpleStdin()
func (cb Broker) SimpleStdin() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()

		if err := scanner.Err(); err != nil {
			log.Fatalf("Failed while reading from stdin: %s\n", err)
		}

		// ignore empty lines
		if len(line) == 0 {
			continue
		}

		cb.Stdin <- line
	}
}

// SimpleStdout prints all replies, etc to the broker on os.Stdout.
// e.g. go cbroker.SimpleStdout()
func (cb Broker) SimpleStdout() {
	for {
		select {
		case txt := <-cb.Stdout:
			// events from the Reply() method go through a go channel
			_, err := os.Stdout.WriteString(txt)
			if err != nil {
				log.Fatalf("Could not write to stdout: %s\n", err)
			}
		}
	}
}

func (cb Broker) Stream(out chan *hal.Evt) {
	for {
		input := <-cb.Stdin

		e := hal.Evt{
			User:     cb.User,
			UserId:   cb.User,
			Room:     cb.Room,
			RoomId:   cb.Room,
			Body:     input,
			Time:     time.Now(),
			Broker:   cb,
			Original: &input,
		}

		if strings.HasPrefix(e.Body, "/") {
			args := e.BodyAsArgv()

			// detect slash commands for creating specialized event types
			switch args[0] {
			case "/reaction":
				if len(args) == 2 {
					e.Body = args[1]
					// re-cast the reaction as a type that can be introspected by plugins
					orig := SlashReaction(args[1])
					e.Original = &orig
				} else {
					e.Reply("/reaction requires exactly one argument!")
				}
			}
		} else {
			// everything else is just a plain chat event
			out <- &e
		}
	}
}

// required by interface
func (b Broker) RoomIdToName(in string) string { return in }
func (b Broker) RoomNameToId(in string) string { return in }
func (b Broker) UserIdToName(in string) string { return in }
func (b Broker) UserNameToId(in string) string { return in }

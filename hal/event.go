package hal

/*
 * Copyright 2016 Albert P. Tobey <atobey@netflix.com>
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Evt is a generic container for events processed by the bot.
// Event sources are responsible for copying the appropriate data into
// the Evt fields. Routing and most plugins will not work if the body
// isn't copied, at a minimum.
// When ToUser and ToRoom are both true, the event will be delivered twice.
// The original event should usually be attached to the Original
type Evt struct {
	ID        string       `json:"id"`      // ID for the event (assigned by upstream or broker)
	Body      string       `json:"body"`    // body of the event, regardless of source
	Command   string       `json:"command"` // optional command associated with the body, typically empty
	Subject   string       `json:"subject"` // the subject of the message, if available, typically empty
	Room      string       `json:"room"`    // the room where the event originated
	RoomId    string       `json:"room_id"` // the room id from the source broker
	User      string       `json:"user"`    // the username that created the event
	UserId    string       `json:"user_id"` // the user id from the source broker
	Time      time.Time    `json:"time"`    // timestamp of the event
	Broker    Broker       `json:"broker"`  // the broker the event came from
	IsChat    bool         `json:"is_chat"` // lets the broker differentiate chats and other events
	IsBot     bool         `json:"is_bot"`  // message was generated by the bot
	ToUser    bool         `json:"to_user"` // when true, always deliver outgoing event via DM
	ToRoom    bool         `json:"to_room"` // when true, always deliver outgoing event to the room
	ToFunc    bool         `json:"to_func"` // when true, call the ReplyFunc instead of the usual reply path
	ReplyFunc func(string) // a function to be called with a reply rather than the usual process
	Original  interface{}  // the original message container (e.g. slack.MessageEvent)
	instance  *Instance    // used by the broker to provide plugin instance metadata
}

// Clone() returns a copy of the event with the same broker/room/user
// and a current timestamp. Body, Command, Subject, and Original will be empty.
func (e *Evt) Clone() Evt {
	out := Evt{
		ID:       e.ID,
		Room:     e.Room,
		RoomId:   e.RoomId,
		User:     e.User,
		UserId:   e.UserId,
		Time:     time.Now(),
		Broker:   e.Broker,
		IsChat:   e.IsChat,
		IsBot:    e.IsBot,
		Original: e.Original,
	}

	return out
}

// Reply is a helper that crafts a new event from the provided string
// and initiates the reply on the broker attached to the event.
// The message is routed according to preferences and the ToUser/ToRoom
// fields on the event. If no preferences are set for the user/room/plugin
// the response will go to the room where the command originated.
// The "reply-via-dm" preference can be set to "true" to default to
// having replies to to DM instead of the room.
func (e *Evt) Reply(msg string) {
	var delivered bool

	if e.ToFunc {
		e.ReplyFunc(msg)
		delivered = true
	}

	if e.ToRoom {
		e.ReplyToRoom(msg)
		delivered = true
	}

	if e.ToUser {
		e.ReplyDM(msg)
		delivered = true
	}

	if delivered {
		return
	}

	replyVia := e.AsPref().FindKey("reply-via-dm").One()

	// One() sets Success to false for no results.
	if replyVia.Success {
		if replyVia.Value == "true" {
			e.ReplyDM(msg)
			return
		}
	}

	// replyVia might be false (or invalid) in which case it falls through to here
	e.ReplyToRoom(msg)
}

// Replyf is the same as Reply but allows for string formatting using
// fmt.Sprintf()
func (e *Evt) Replyf(msg string, a ...interface{}) {
	e.Reply(fmt.Sprintf(msg, a...))
}

// Error replies to the event with the provided error.
// Future: need to figure out if there's going to be a kind of error
// handling module in Hal for making errors visible in a logging room,
// possibly on a different broker...
func (e *Evt) Error(err error) {
	e.Reply(fmt.Sprintf("%s", err))
}

// ReplyTable sends a table of data back, formatting it according to
// preferences.
// TODO: move code from brokers/slack/broker.go/SendTable here
// TODO: document preferences here
func (e *Evt) ReplyTable(hdr []string, rows [][]string) {
	out := e.Clone() // may not be necessary

	if e.Broker != nil {
		e.Broker.SendTable(out, hdr, rows)
	} else {
		panic("hal.Evt.ReplyTable called with nil Broker!")
	}
}

// ReplyDM makes it convenient to reply to a user via DM. The user is drawn
// from the event's UserId field and passed to the broker's SendDM() method.
func (e *Evt) ReplyDM(msg string) {
	out := e.Clone()
	out.Body = msg
	e.Broker.SendDM(out)
}

// ReplyToRoom crafts a new event from the provided string
// and sends it to the room the event originated from.
func (e *Evt) ReplyToRoom(msg string) {
	out := e.Clone()
	out.Body = msg

	if e.Broker != nil {
		e.Broker.Send(out)
	} else {
		panic("hal.Evt.Reply called with nil Broker!")
	}
}

// BrokerName returns the text name of current broker.
func (e *Evt) BrokerName() string {
	return e.Broker.Name()
}

// FindPrefs fetches the union of all matching settings from the database
// for user, broker, room, and plugin.
// Plugins can use the Prefs methods to filter from there.
func (e *Evt) FindPrefs() Prefs {
	broker := e.BrokerName()
	plugin := e.instance.Plugin.Name
	return FindPrefs(e.User, broker, e.RoomId, plugin, "")
}

// InstanceSettings gets all the settings matching the settings defined
// by the plugin's Settings field.
func (e *Evt) InstanceSettings() Prefs {
	broker := e.BrokerName()
	plugin := e.instance.Plugin.Name

	out := make(Prefs, 0)

	for _, stg := range e.instance.Plugin.Settings {
		// ignore room-specific settings for other rooms
		if stg.Room != "" && stg.Room != e.RoomId {
			continue
		}

		pref := GetPref("", broker, e.RoomId, plugin, stg.Key, stg.Default)
		out = append(out, pref)
	}

	return out
}

// AsPref returns a a pref with user, room, broker, and plugin set using data
// from the event handle.
func (e *Evt) AsPref() Pref {
	// AsPref can be called without an instance for errors, make sure
	// instance is set before accessing fields
	var plugin string
	if e.instance != nil {
		plugin = e.instance.Plugin.Name
	}

	p := Pref{
		User:   e.UserId,
		Room:   e.RoomId,
		Broker: e.BrokerName(),
		Plugin: plugin,
	}

	return p
}

// BodyAsArgv does minimal parsing of the event body, returning an argv-like
// array of strings with quoted strings intact (but with quotes removed).
// The goal is shell-like, and is not a full implementation.
// Leading/trailing whitespace is removed.
// Escaping quotes, etc. is not supported.
func (e *Evt) BodyAsArgv() []string {
	// use a simple RE rather than pulling in a package to do this
	re := regexp.MustCompile(`'[^']*'|"[^"]*"|\S+`)
	body := strings.TrimSpace(e.Body)
	argv := re.FindAllString(body, -1)

	// remove the outer quotes from quoted strings
	for i, val := range argv {
		if strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`) {
			tmp := strings.TrimPrefix(val, `'`)
			argv[i] = strings.TrimSuffix(tmp, `'`)
		} else if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
			tmp := strings.TrimPrefix(val, `"`)
			argv[i] = strings.TrimSuffix(tmp, `"`)
		}
	}

	return argv
}

// ForceToRoom clones the event and returns a copy with ToRoom set to true.
// Takes priority over reply-via-dm routing.
// Useful for chaining, e.g. evt.ToRoom().Replyf("go away!").
func (e *Evt) ForceToRoom() Evt {
	out := e.Clone()
	out.ToRoom = true
	return out
}

// ForceToUser clones the event and returns a copy with ToUser set to true.
// Takes priority over reply-via-dm routing.
// Useful for chaining.
func (e *Evt) ForceToUser() Evt {
	out := e.Clone()
	out.ToUser = true
	return out
}

func (e *Evt) String() string {
	return fmt.Sprintf("User: %q Room: %q Time: %q Body: %q", e.User, e.Room, e.Time.String(), e.Body)
}

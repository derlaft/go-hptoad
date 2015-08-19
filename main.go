package main

import (
	"fmt"
	"github.com/derlaft/xmpp"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	room     = "room@conference.example.com"
	name     = "botname"
	server   = "example.com"
	me       = name + "@" + server
	id       = name
	password = "password"
	resource = "resource"
	connect  = "xmpp.example.com:5222"
)

var (
	admin []string
	cs    = make(chan xmpp.Stanza)
	stop  chan struct{}
	next  xmpp.Stanza
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	var (
		Conn *xmpp.Conn
		err  error
	)

START:
	for {
		stop = make(chan struct{})
		if(err != nil) {
			admin = admin[:0]
			if Conn != nil {
				log.Println("Conn check:", Conn.Close())
			}
			time.Sleep(5 * time.Second)
		}

		Conn, err = xmpp.Dial(connect, id, server, password, resource, nil)
		if err != nil {
			log.Println("Conn", err)
			continue
		}
		if err := Conn.SignalPresence("dnd", "is there some food in this world?", 12); err != nil {
			log.Println("Signal", err)
			continue
		}
		if err := Conn.SendPresence(room + "/" + name, ""); err != nil {
			log.Println("Presence", err)
			continue
		}

		go func(Conn *xmpp.Conn, stop chan struct{}) {
			for {
				select {
				case <-time.After(60 * time.Second):
					Conn.SendIQ(server, "set", "<keepalive xmlns='urn:xmpp:keepalive:0'> <interval>60</interval> </keepalive>")
					if _, _, err = Conn.SendIQ(server, "get", "<ping xmlns='urn:xmpp:ping'/>"); err != nil {
						select {
						case <-stop:
						default:
							log.Println("KeepAlive err:", err)
							close(stop)
						}
						return
					}
					
				case <-stop:
					return
				}
			}
		}(Conn, stop)

		go func(Conn *xmpp.Conn, stop chan struct{}) {
			for {
				next, err := Conn.Next()
				if err != nil {
					select {
					case <-stop:
					default:
						log.Println("KeepAlive err:", err)
						close(stop)
					}
					return
				}
				cs <- next
			}
		}(Conn, stop)

		for {
			select {
			case next = <-cs:
				
			case <-stop:
				Conn.Close()
				Conn = nil
				continue START
				
			case <-time.After(65 * time.Second):
				log.Println(Conn.Close(), "\n\t", "closed after 65 seconds of inactivity")
				close(stop)
				Conn = nil
				continue START
			}
			
			switch t := next.Value.(type) {
			case *xmpp.ClientPresence:
				PresenceHandler(Conn, t)
				
			case *xmpp.ClientMessage:
				if len(t.Delay.Stamp) == 0 && len(t.Subject) == 0 {
					log.Println(t)
					if GetNick(t.From) != name {
						if t.Type == "groupchat" {
							go MessageHandler(Conn, t)
						} else if xmpp.RemoveResourceFromJid(strings.ToLower(t.From)) == me {
							go SelfHandler(Conn, t)
						}
					}
				}
			}
		}
		log.Println(Conn.Close(), "\n\t", "wtf am I doing here?")
	}
}

func SelfHandler(Conn *xmpp.Conn, Msg *xmpp.ClientMessage) {
	Msg.Body = strings.TrimSpace(Msg.Body)
	if(!strings.HasPrefix(Msg.Body, "!")) {
		Conn.Send(room, "groupchat", Msg.Body)
		return
	}
	command, err := GetCommand(Msg.Body, Msg.From, "./plugins/")
	if(err != nil) {
		Conn.Send(Msg.From, "chat", err.Error())
		return
	}
	out, err := command.CombinedOutput()
	if err != nil {
		log.Println(err)
		Conn.Send(Msg.From, "chat", err.Error())
		return
	}
	Conn.Send(Msg.From, "chat", strings.TrimRight(string(out), " \t\n"))
}

var call = regexp.MustCompile("^" + name + "[:,]")
func MessageHandler(Conn *xmpp.Conn, Msg *xmpp.ClientMessage) {
	switch {
	case strings.HasPrefix(Msg.Body, "!megakick "):
		s := strings.Split(Msg.Body, "!megakick ")
		if in(admin, Msg.From) {
			Conn.ModUse(room, s[1], "none", "")
		} else {
			Conn.Send(room, "groupchat", fmt.Sprintf("%s: GTFO", GetNick(Msg.From)))
		}
		
	case strings.HasPrefix(Msg.Body, "!"): //any external command
		cmd, err := GetCommand(Msg.Body, Msg.From, "./plugins/")
		if(err != nil) {
			Conn.Send(room, "groupchat", fmt.Sprintf("%s: WAT", GetNick(Msg.From)))
			if(in(admin, Msg.From)) {
				Conn.Send(Msg.From, "chat", err.Error())
			}
			return
		}
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			log.Println(err)
			Conn.Send(room, "groupchat", fmt.Sprintf("%s: WAT", GetNick(Msg.From)))
			if(in(admin, Msg.From)) {
				Conn.Send(Msg.From, "chat", err.Error())
			}
			return
		}
		out, _ := ioutil.ReadAll(stdout)
		outerr, _ := ioutil.ReadAll(stderr)
		cmd.Wait()
		if len(outerr) != 0 && in(admin, Msg.From) {
			Conn.Send(Msg.From, "chat", strings.TrimRight(string(outerr), " \t\n"))
		}
		Conn.Send(room, "groupchat", strings.TrimRight(string(out), " \t\n"))
		
	case call.MatchString(Msg.Body): //chat
		command, err := GetCommand(call.ReplaceAllString(Msg.Body, "!answer"), Msg.From, "./chat/")
		if err != nil {
			log.Println(err)
			return
		}
		out, err := command.CombinedOutput()
		if err != nil {
			log.Println(err)
			if(in(admin, Msg.From)) {
				Conn.Send(Msg.From, "chat", err.Error())
			}
			return
		}
		Conn.Send(room, "groupchat", strings.TrimRight(string(out), " \t\n"))
	}
}

func PresenceHandler(Conn *xmpp.Conn, Prs *xmpp.ClientPresence) {
	if(Prs.From == room + "/" + name && Prs.Item.Role == "none") {
		log.Println("was kicked")
		close(stop)
		return
	}
	switch Prs.Item.Affiliation {
	case "owner":
		fallthrough
	case "admin":
		if Prs.Item.Role != "none" {
			if !in(admin, Prs.From) {
				admin = append(admin, Prs.From)
			}
		}
	default:
		if in(admin, Prs.From) {
			admin = del(admin, Prs.From)
		}
	}
}

//letter(ASCII or cyrillic), number, underscore only
var cmd_validator = regexp.MustCompile("^!(\\w|\\p{Cyrillic})*$")
func GetCommand(body, from, dir string) (*exec.Cmd, error) {
	split := strings.SplitAfterN(body, " ", 2)
	cmd := strings.TrimSpace(split[0])
	
	if(!cmd_validator.MatchString(cmd)) { return nil, fmt.Errorf("Bad command \"%s\"", cmd) }
	
	var (
		info os.FileInfo
		err error
	)
	path := dir + Strip(cmd[1:])
	if info, err = os.Stat(path); err != nil { return nil, err }
	if info.IsDir() || info.Mode() & 0111 == 0 { return nil, fmt.Errorf("\"%s\" isn't executable", path) }
	
	args := []string{ Strip(GetNick(from)), strconv.FormatBool(in(admin, from)) }
	if(len(split) > 1) { args = append(args, Strip(split[1])) }
	return exec.Command(path, args...), nil
}

var strip_regexp = regexp.MustCompile("(`|\\$|\\.\\.)")
var quote_regexp = regexp.MustCompile("(\"|')")
func Strip(s string) string {
	return quote_regexp.ReplaceAllString(strip_regexp.ReplaceAllString(s, ""), "“")
}

func GetNick(s string) string {
	slash := strings.Index(s, "/")
	if slash != -1 {
		return s[slash+1:]
	}
	return s
}

func in(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}

func pos(slice []string, value string) int {
	for p, v := range slice {
		if v == value {
			return p
		}
	}
	return -1
}

func del(slice []string, value string) []string {
	if i := pos(slice, value); i >= 0 {
		return append(slice[:i], slice[i+1:]...)
	}
	return slice
}

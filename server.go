package collectdclone

import (
	"bytes"
	"fmt"
	"github.com/howeyc/fsnotify"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Message struct {
	Name      string
	Value     string
	Timestamp int64
}

type Service struct {
	conn      net.Conn
	apiKey    string
	directory string
	scripts   map[string]int
	messages  chan Message
}

func isExecutable(name string) bool {
	info, err := os.Stat(name)
	if err != nil {
		return false
	}
	return info.Mode()&0111 != 0
}

func expandPluginDirectory(name, directory string) string {
	return directory + "/" + name
}

func getScriptName(name, directory string) string {
	if strings.HasPrefix(directory, "./") {
		directory = directory[2:]
	}
	return strings.TrimPrefix(name, directory+"/")
}

func NewService(apiKey, address string, directory string) (*Service, error) {

	w, err := fsnotify.NewWatcher()

	if err != nil {
		return nil, err
	}

	err = w.Watch(directory)

	if err != nil {
		return nil, err
	}

	s := Service{
		scripts:   make(map[string]int),
		directory: directory,
		apiKey:    apiKey,
		messages:  make(chan Message),
	}

	files, _ := ioutil.ReadDir(directory)
	for _, f := range files {
		if isExecutable(expandPluginDirectory(f.Name(), directory)) {
			s.scripts[f.Name()] = 0
		}
	}

	log.Println("%s", address)

	s.conn, err = net.DialTimeout("tcp", address, 30*time.Second)

	if err != nil {
		return nil, err
	}

	go func() {
		for {
			select {
			case ev := <-w.Event:
				name := getScriptName(ev.Name, directory)
				if ev.IsCreate() {
					if isExecutable(expandPluginDirectory(name, directory)) {
						s.scripts[name] = 0
					}
				}
				if ev.IsDelete() {
					delete(s.scripts, name)
				}

			case err := <-w.Error:
				log.Println("error:", err)
			}
		}
	}()

	return &s, nil
}

func (s *Service) RunScript(scriptName string) {

	execmd := fmt.Sprintf("./%s/%s", s.directory, scriptName)

	cmd := exec.Command(execmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}
	parts := strings.Split(out.String(), " ")

	if len(parts) != 2 {
		log.Fatalf("Invalid script output: %s", out.String())
	}

	s.messages <- Message{
		Name:      parts[0],
		Value:     parts[1],
		Timestamp: time.Now().Unix(),
	}

}

func (s *Service) Run() {

	go func() {
		for m := range s.messages {
			msg := fmt.Sprintf("%s.%s %s %d\n",
				s.apiKey, m.Name, m.Value, m.Timestamp)
			buf := bytes.NewBufferString(msg)
			_, err := s.conn.Write(buf.Bytes())
			if err != nil {
				log.Fatal(err)
			}
		}
	}()

	c := time.Tick(1 * time.Second)
	for now := range c {
		for script, _ := range s.scripts {
			log.Printf("Running %s at %s\n.", script, now)
			go s.RunScript(script)
		}
	}
}
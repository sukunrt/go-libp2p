package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var selectorRegexp = regexp.MustCompile(`libp2p_(?P<metric>[A-Za-z0-9_]*){(?P<selector>[^}]*)}`)
var noSelectorRegexp = regexp.MustCompile(`libp2p_(?P<metric>[A-Za-z0-9_]*)`)

var files = []string{
	"../swarm/swarm.json",
	"../autonat/autonat.json",
	"../autorelay/autorelay.json",
	"../eventbus/eventbus.json",
	"../identify/identify.json",
	"../relaysvc/relaysvc.json",
	"../resource-manager/resource-manager.json",
}

func main() {
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			fmt.Println(err)
			return
		}
		s := bufio.NewScanner(f)
		intermediate := []string{}
		for s.Scan() {
			line := s.Text()
			if strings.Contains(line, "regex") {
				continue
			}
			intermediate = append(intermediate, transformSelector(line))
		}
		out := strings.Builder{}
		for _, line := range intermediate {
			out.WriteString(transformNoSelector(line))
			out.WriteString("\n")
		}
		name := f.Name()
		_, name = filepath.Split(name)
		outfile, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, os.ModePerm)
		if err != nil {
			fmt.Println(err)
			return
		}
		outfile.Write([]byte(out.String()))
	}
}

func transformSelector(line string) string {
	matches := selectorRegexp.FindAllStringSubmatchIndex(line, -1)
	i := 0
	out := strings.Builder{}
	for j := 0; j < len(matches); j++ {
		metric := line[matches[j][2]:matches[j][3]]
		selector := line[matches[j][4]:matches[j][5]]
		out.WriteString(line[i:matches[j][2]])
		out.WriteString(metric)
		out.WriteString("{" + selector + `,instance=~\"$instance\"}`)
		i = matches[j][1]
	}
	out.WriteString(line[i:])
	return out.String()
}

func transformNoSelector(line string) string {
	out := strings.Builder{}
	matches := noSelectorRegexp.FindAllStringSubmatchIndex(line, -1)
	i := 0
	for j := 0; j < len(matches); j++ {
		end := matches[j][1]
		found := false
		for x := end; x < len(line); x++ {
			if unicode.IsSpace(rune(line[x])) {
				continue
			}
			if line[x] == '{' {
				found = true
			} else {
				found = false
			}
			break
		}
		if found {
			continue
		}
		metric := line[matches[j][2]:matches[j][3]]
		out.WriteString(line[i:matches[j][2]])
		out.WriteString(metric)
		out.WriteString(`{instance=~\"$instance\"}`)
		out.WriteString(line[matches[j][3]:matches[j][1]])
		i = matches[j][1]
	}
	out.WriteString(line[i:])
	return out.String()
}

package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type config struct {
	Token    string `json:"token"`
	WaitTime int    `json:"wait_time"`
}

var cfg config

var pings map[string]*ping

var pingRoles map[string]string
var pingRolesLock sync.RWMutex

func init() {
	pings = make(map[string]*ping)
	pingRoles = make(map[string]string)
}

func main() {
	b, err := ioutil.ReadFile("config.json")
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(b, &cfg)
	if err != nil {
		panic(err)
	}

	b, err = ioutil.ReadFile("roles.json")
	if err != nil && !os.IsNotExist(err) {
		panic(err)
	}
	if len(b) > 0 {
		err = json.Unmarshal(b, &pingRoles)
		if err != nil {
			panic(err)
		}
	}

	d, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		panic(err)
	}

	d.AddHandler(messageCreate)

	err = d.Open()
	if err != nil {
		panic(err)
	}
	defer d.Close()

	defer savePingRoles()

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, os.Interrupt, os.Kill)
	<-sc
}

func savePingRoles() {
	pingRolesLock.Lock()
	defer pingRolesLock.Unlock()

	b, err := json.Marshal(pingRoles)
	if err != nil {
		panic(err)
	}

	err = ioutil.WriteFile("roles.json", b, 0644)
	if err != nil {
		panic(err)
	}
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if strings.ToLower(m.Content) == "stop" {
		var found bool
		for userID, ping := range pings {
			if ping.ChannelID == m.ChannelID && (ping.AuthorID == m.Author.ID || userID == m.Author.ID) {
				found = true
				ping.running = false
				delete(pings, userID)
				s.ChannelMessageSend(m.ChannelID, "okay")
				break
			}
		}
		if !found {
			s.ChannelMessageSend(m.ChannelID, "nah")
		}

	} else if strings.HasPrefix(m.Content, "@pingroledel") {
		s.ChannelMessageSend(m.ChannelID, delPingRole(s, m))
	} else if strings.HasPrefix(m.Content, "@pingrole") {
		s.ChannelMessageSend(m.ChannelID, setPingRole(s, m))
	} else if strings.HasPrefix(m.Content, "@ping") {
		s.ChannelMessageSend(m.ChannelID, parsePing(s, m))
	}
}

func isNum(c rune) bool {
	return c >= 48 && c <= 57
}

func parsePing(s *discordgo.Session, m *discordgo.MessageCreate) string {
	args := strings.Split(m.Content, " ")
	if len(args) < 2 {
		return "usage: @ping <target> [interval]"
	}

	pingRoleID, ok := pingRoles[m.GuildID]
	if ok {
		found := false
		for _, roleID := range m.Member.Roles {
			if roleID == pingRoleID {
				found = true
			}
		}
		if !found {
			return "you don't have permission to ping people"
		}
	}

	mention := args[1]
	match := regexp.MustCompile(`<@!?\d+>`).MatchString(mention)
	if !match {
		return "invalid target"
	}
	userID := mention[strings.IndexFunc(mention, isNum) : len(mention)-1]
	if userID == m.Author.ID {
		return "you can't ping yourself silly"
	}

	member, err := s.State.Member(m.GuildID, userID)
	if err != nil {
		return "invalid mention"
	} else if member.User.Bot {
		return "no pinging bots"
	} else if pings[userID] != nil {
		return "they're already being pinged!"
	}

	wait := time.Duration(cfg.WaitTime) * time.Second
	if len(args) == 3 {
		i, err := strconv.ParseInt(args[2], 10, 8)
		if err != nil {
			return "error converting interval: " + err.Error()
		} else if i < 2 {
			return "invalid interval: minimum of 2 seconds required"
		}
		wait = time.Duration(i) * time.Second
	}

	pings[userID] = &ping{ChannelID: m.ChannelID, AuthorID: m.Author.ID, WaitTime: wait}
	go pings[userID].Run(s, mention)

	return "added ping :)"
}

type ping struct {
	ChannelID string
	AuthorID  string
	WaitTime  time.Duration
	running   bool
}

func (p *ping) Run(s *discordgo.Session, mention string) {
	p.running = true
	time.Sleep(time.Second)

	for p.running {
		s.ChannelMessageSend(p.ChannelID, mention)
		time.Sleep(p.WaitTime)
	}
}

func isAdmin(userRoles []string, guildRoles []*discordgo.Role) bool {
	for _, roleID := range userRoles {
		for _, role := range guildRoles {
			if role.ID == roleID && role.Permissions&discordgo.PermissionAdministrator == 0 {
				return true
			}
		}
	}
	return false
}

func setPingRole(s *discordgo.Session, m *discordgo.MessageCreate) string {
	guild, _ := s.State.Guild(m.GuildID)
	if !isAdmin(m.Member.Roles, guild.Roles) {
		return "you're not an admin so you can't do this"
	}

	args := strings.Split(m.Content, " ")
	if len(args) != 2 {
		return "usage: @pingrole <roleID>"
	}

	if strings.IndexFunc(args[1], func(c rune) bool { return !isNum(c) }) != -1 {
		return "invalid roleID"
	} else if _, err := s.State.Role(m.GuildID, args[1]); err != nil {
		return "invalid roleID"
	}

	pingRolesLock.Lock()
	defer pingRolesLock.Unlock()

	pingRoles[m.GuildID] = args[1]

	return "set ping role :)"
}

func delPingRole(s *discordgo.Session, m *discordgo.MessageCreate) string {
	guild, _ := s.State.Guild(m.GuildID)
	if !isAdmin(m.Member.Roles, guild.Roles) {
		return "you're not an admin so you can't do this"
	}

	pingRolesLock.Lock()
	defer pingRolesLock.Unlock()

	delete(pingRoles, m.GuildID)

	return "deleted ping role; anybody can @ping now"
}

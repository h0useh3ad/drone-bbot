package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/lair-framework/api-server/client"
	"github.com/lair-framework/go-lair"
)

const (
	version = "1.0.0"
	tool    = "drone-bbot"
	usage   = `
Parses a bbot JSON file into a Lair project, extracting DNS name and IP.

Usage:
  drone-bbot [options] <id> <filename>
  export LAIR_ID=<id>; drone-bbot [options] <filename>
Options:
  -v              show version and exit
  -h              show usage and exit
  -k              allow insecure SSL connections
  -force-hosts    import all hosts into Lair, default behaviour is to only import
                  DNS records for hosts that already exist in the project
  -tags           a comma separated list of tags to add to every host that is imported
`
)

func main() {
	showVersion := flag.Bool("v", false, "")
	insecureSSL := flag.Bool("k", false, "")
	forceHosts := flag.Bool("force-hosts", false, "")
	tags := flag.String("tags", "", "")
	flag.Usage = func() {
		fmt.Print(usage)
	}
	flag.Parse()
	if flag.NArg() < 2 {
		log.Fatal("Fatal: Missing required arguments <id> and <filename>")
	}
	lairPID := flag.Arg(0)
	filename := flag.Arg(1)

	if *showVersion {
		log.Println(version)
		os.Exit(0)
	}

	lairURL := os.Getenv("LAIR_API_SERVER")
	if lairURL == "" {
		log.Fatal("Fatal: Missing LAIR_API_SERVER environment variable")
	}

	u, err := url.Parse(lairURL)
	if err != nil {
		log.Fatalf("Fatal: Error parsing LAIR_API_SERVER URL. Error %s", err.Error())
	}

	user := u.User.Username()
	pass, _ := u.User.Password()
	if user == "" || pass == "" {
		log.Fatal("Fatal: Missing username and/or password")
	}

	c, err := client.New(&client.COptions{
		User:               user,
		Password:           pass,
		Host:               u.Host,
		Scheme:             u.Scheme,
		InsecureSkipVerify: *insecureSSL,
	})
	if err != nil {
		log.Fatalf("Fatal: Error setting up client: Error %s", err.Error())
	}

	existingProject, err := c.ExportProject(lairPID)
	if err != nil {
		log.Fatalf("Fatal: Unable to export project. Error %s", err.Error())
	}

	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Fatal: Could not open file. Error %s", err.Error())
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	hostTags := []string{}
	if *tags != "" {
		hostTags = strings.Split(*tags, ",")
	}

	project := &lair.Project{
		ID:   lairPID,
		Tool: tool,
		Commands: []lair.Command{
			{Tool: tool},
		},
	}

	existingIPs := make(map[string]lair.Host)
	for _, host := range existingProject.Hosts {
		existingIPs[host.IPv4] = host
	}

	bNotFound := make(map[string][]string)

	for scanner.Scan() {
		line := scanner.Text()

		var entry map[string]interface{}
		err = json.Unmarshal([]byte(line), &entry)
		if err != nil {
			log.Fatalf("Fatal: Could not parse BBot JSON. Error %s", err.Error())
		}

		if entry["type"] == "DNS_NAME" {
			dnsName := entry["host"].(string)
			resolvedHosts := entry["resolved_hosts"].([]interface{})
			for _, ip := range resolvedHosts {
				ipStr := ip.(string)

				if existingHost, found := existingIPs[ipStr]; found {
					existingHost.Hostnames = append(existingHost.Hostnames, dnsName)
					existingHost.LastModifiedBy = tool
					existingHost.Tags = append(existingHost.Tags, hostTags...)
					existingIPs[ipStr] = existingHost
				} else {
					if *forceHosts {
						project.Hosts = append(project.Hosts, lair.Host{
							IPv4:           ipStr,
							Hostnames:      []string{dnsName},
							Tags:           hostTags,
							LastModifiedBy: tool,
						})
					} else {
						bNotFound[ipStr] = append(bNotFound[ipStr], dnsName)
					}
				}
			}
		}
	}

	for _, host := range existingIPs {
		project.Hosts = append(project.Hosts, host)
	}

	if len(project.Hosts) > 0 {
		options := &client.DOptions{}
		res, err := c.ImportProject(options, project)
		if err != nil {
			log.Fatalf("Fatal: Unable to import project. Error %s", err)
		}
		defer res.Body.Close()
		log.Println("Success: Operation completed successfully")
	} else {
		log.Println("No new hosts were imported.")
	}

	if len(bNotFound) > 0 {
		log.Println("The following hosts had DNS names but could not be imported because they do not exist in lair:")
		for ip, dnsNames := range bNotFound {
			log.Printf("IP: %s, DNS Names: %v\n", ip, dnsNames)
		}
	}
}

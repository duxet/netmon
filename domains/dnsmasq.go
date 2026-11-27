package domains

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Logread is the dns_log that follows OpenWrt's system log, where dnsmasq
// logs with logqueries on.
const Logread = "logread"

// dnsmasqParser learns names from the lines dnsmasq logs with log-queries:
//
//	reply www.netflix.com is <CNAME>
//	reply ipv4-c001.1.lhr004.ix.nflxvideo.net is 45.57.40.1
//	cached www.google.com is 142.250.186.78
//
// An answer that goes through CNAMEs is logged as one line per name of the
// chain, the name asked for first, so an address gets that name too.
type dnsmasqParser struct {
	names *Names
	// head is the name asked for of the chain the last line was part of.
	head string
}

// line reads one line of the log, as syslog or logread writes it, with
// whatever comes before "dnsmasq[pid]: ".
func (p *dnsmasqParser) line(line string, at time.Time) {
	i := strings.Index(line, "dnsmasq[")
	if i < 0 {
		p.head = ""
		return
	}
	j := strings.Index(line[i:], "]: ")
	if j < 0 {
		p.head = ""
		return
	}
	fields := strings.Fields(line[i+j+3:])
	if len(fields) != 4 || (fields[0] != "reply" && fields[0] != "cached") || fields[2] != "is" {
		p.head = ""
		return
	}

	name, answer := fields[1], fields[3]
	if answer == "<CNAME>" {
		if p.head == "" {
			p.head = name
		}
		return
	}
	addr, err := netip.ParseAddr(answer)
	if err != nil {
		// NXDOMAIN, NODATA and the like end the chain.
		p.head = ""
		return
	}
	p.names.Observe(addr, name, at)
	if p.head != "" {
		p.names.Observe(addr, p.head, at)
	}
}

// Follow learns names from dnsmasq's log until ctx is done: from a file it
// follows as tail -F does, or from OpenWrt's system log with Logread.
func Follow(ctx context.Context, source string, names *Names) {
	follow := followFile
	if source == Logread {
		follow = followLogread
	}
	for {
		err := follow(ctx, source, &dnsmasqParser{names: names})
		if ctx.Err() != nil {
			return
		}
		slog.Warn("Stopped reading the DNS log, trying again in a minute", "source", source, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func followLogread(ctx context.Context, _ string, p *dnsmasqParser) error {
	cmd := exec.CommandContext(ctx, "logread", "-f", "-e", "dnsmasq")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	slog.Info("Reading DNS answers from the system log")
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		p.line(scanner.Text(), time.Now())
	}
	err = cmd.Wait()
	if err == nil {
		err = errors.New("logread ended")
	}
	return err
}

// followFile reads the lines added to a file from its end on, and from the
// start of the file that replaces it when it is rotated or truncated.
func followFile(ctx context.Context, path string, p *dnsmasqParser) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { file.Close() }()
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	slog.Info("Reading DNS answers from a log", "file", path)

	reader := bufio.NewReader(file)
	var partial string
	for {
		line, err := reader.ReadString('\n')
		offset += int64(len(line))
		if err == nil {
			p.line(partial+line, time.Now())
			partial = ""
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}
		partial += line

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}

		// A rotated or truncated log starts over.
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		current, _ := file.Stat()
		if !os.SameFile(info, current) || info.Size() < offset {
			reopened, err := os.Open(path)
			if err != nil {
				continue
			}
			file.Close()
			file, reader, offset, partial = reopened, bufio.NewReader(reopened), 0, ""
		}
	}
}

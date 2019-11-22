/* ======================================================================== */
/* logpipe.go - STDIN to UNIX-domain socket log writer                      */
/* Written by Chase Venters <chase.venters@gmail.com>, Public Domain        */
/* ======================================================================== */

package main

import (
	"bufio"
	"flag"
	"log"
	"fmt"
	"net"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"encoding/json"
)

type jsonAttrSet map[string]string
var jsonAttrs = make(jsonAttrSet)

// Define flags
var f_logpath = flag.String("lp-logfile", "", "Path to the logpipe log")
var f_socketpath = flag.String("socket", "", "Path to the log socket")
var f_prefix = flag.String("prefix", "", "Prefix to add to lines")
var f_socket_type = flag.String("socket-type", "stream",
	"Type of UNIX-domain socket connection (stream/dgram)")
var f_reconnect_time = flag.Int("reconnect-time", 1,
	"Time to wait (in seconds) before reconnect")
var f_wrap = flag.Int("wrap", 0, "Characters after which to wrap the message")
var f_init_reconnect = flag.Bool("retry-initial-connect", true,
	"Try reconnecting if the initial connection fails")
var f_esc_null = flag.Bool("escape-null", true,
	"Escapes NULL characters in output as <NUL>")
var f_output_mode = flag.String("output-mode", "line", "Output mode (line/json)")

func (i *jsonAttrSet) String() string {
	r, err := json.Marshal(*i)
	if err != nil {
		return fmt.Sprintf("jsonAttrSet Marshal error: %v", err)
	} else {
		return string(r)
	}
}

func (i *jsonAttrSet) Set(value string) error {
	p := strings.SplitN(value, "=", 2)
	if len(p) < 2 {
		log.Fatalf("-json-attr '%s' must be specified as k=v pair", value)
	}
	k := p[0]
	v := p[1]
	if _, found := (*i)[k]; found {
		log.Fatalf("-json-attr '%s' specified multiple times", k)
	}
	(*i)[k] = v
	return nil
}

// Initialize
func main() {
	flag.Var(&jsonAttrs, "json-attr", "One or more k=v pairs to include " +
		"in output messages")
	flag.Parse()

	// Log exit due to signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("Received signal %v", sig)
		os.Exit(0)
	}()

	// Add PID to log
	log.SetPrefix(fmt.Sprintf("[%d] ", os.Getpid()))

	if *f_socketpath == "" {
		log.Fatal("-socket is a required argument")
	}

	var socktype string
	if *f_socket_type == "stream" {
		socktype = "unix"
	} else if *f_socket_type == "dgram" {
		socktype = "unixgram"
	} else {
		log.Fatal("-socket-type must be stream or dgram")
	}

	if *f_output_mode == "line" {
		if len(jsonAttrs) > 0 {
			log.Fatal("-json-attr cannot be specified unless -output-mode is json")
		}
	} else if *f_output_mode == "json" {
	} else {
		log.Fatalf("-output-mode '%s' must be line or json", *f_output_mode)
	}

	if *f_logpath != "" {
		logfile, err := os.OpenFile(*f_logpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			log.Fatalf("Error opening logfile(%s): %v", *f_logpath, err)
		}
		defer logfile.Close()

		log.SetOutput(io.MultiWriter(os.Stderr, logfile))

		// Dump arguments
		log.Printf("Opened lp-logfile %s", *f_logpath)
		log.Printf("Options:")
		log.Printf("\tsocket='%s' (%s)", *f_socketpath, *f_socket_type)
		log.Printf("\treconnect-time=%d", *f_reconnect_time)
		log.Printf("\tretry-initial-connect=%v", *f_init_reconnect)
		log.Printf("\tprefix='%s'", *f_prefix)
		log.Printf("\twrap=%d", *f_wrap)
		log.Printf("\tescape-null=%v", *f_esc_null)
		log.Printf("\toutput-mode=%s", *f_output_mode)
		log.Printf("\tjson-attr=%s", jsonAttrs.String())
	}

	for {
		run(*f_socketpath, socktype, *f_prefix)
		if *f_reconnect_time > 0 {
			log.Printf(
				"Pausing %d seconds until reconnect", *f_reconnect_time)
			time.Sleep(time.Duration(*f_reconnect_time) * time.Second)
		} else {
			// No reconnect: Bail with error
			os.Exit(1)
		}
	}
}

var nr_conns = 0
var strout string

func makeOutString(instr string) string {
	if *f_output_mode == "line" {
		return instr
	} else {
		jsonAttrs["message"] = instr
		o, err := json.Marshal(jsonAttrs)
		if err != nil {
			log.Fatalf("json Marshal error: %v", err)
		}
		return string(o)
	}
}

func run(socketpath string, sockettype string, prefix string) {
	// Connect to UNIX-domain socket
	conn, err := net.Dial(sockettype, socketpath)
	if err != nil {
		log.Print("Connection failed: ", err.Error())
		if nr_conns == 0 && !*f_init_reconnect {
			// No successful connections have happened, so we haven't
			// read anything from STDIN and we can safely exit now.
			os.Exit(1)
		} else {
			return
		}
	}
	nr_conns++
	log.Printf("Connected to socket %v (#%d)", socketpath, nr_conns)

	// Precompute prefix length. Include newline if we are in line output mode
	var plen = len(prefix)
	if *f_output_mode == "line" {
		plen = plen + 1
	}

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(conn)

	// Keep writing data
	var readerErr error
	for {
		// Build output string
		if strout != "" {
			// Write string to output buffer
			_, err = writer.WriteString(strout)
			if err != nil {
				log.Print("Write failed: ", err.Error())
				return
			}
			err = writer.Flush()
			if err != nil {
				log.Print("Flush failed: ", err.Error())
				return
			}
		}

		// If we didn't get any more data, exit the loop
		var stxt string
		stxt, readerErr = reader.ReadString('\n')
		if len(stxt) < 1 {
			break;
		}
		stxt = stxt[:len(stxt)-1]

		// Escape NULLs in output string
		if *f_esc_null {
			stxt = strings.Replace(stxt, "\x00", "<NUL>", -1);
		}

		if *f_wrap == 0 || len(stxt) + plen < *f_wrap {
			// Queue data for writing
			strout = makeOutString(prefix + stxt) + "\n"
		} else {
			// Prepare string builders
			var sb strings.Builder
			var ob strings.Builder
			sb.WriteString(prefix)
			var lineBytes = plen

			// Wrap stxt, respecting UTF-8 rune boundaries
			for _, runeValue := range stxt {
				var runeBytes = utf8.RuneLen(runeValue)
				if lineBytes + runeBytes > *f_wrap {
					ostr := makeOutString(sb.String())
					ob.WriteString(ostr)
					ob.WriteString("\n")
					sb.Reset()
					sb.WriteString(prefix)
					lineBytes = plen
				}
				lineBytes += runeBytes
				sb.WriteRune(runeValue)
			}
			ostr := makeOutString(sb.String())
			ob.WriteString(ostr)
			ob.WriteString("\n")

			// Flush string
			strout = ob.String()
		}
	}

	if readerErr != nil && readerErr != io.EOF {
		log.Fatal(err)
	}

	// On EOF, we just bail...
	log.Print("Reached EOF on STDIN")
	os.Exit(0)
}


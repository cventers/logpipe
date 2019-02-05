/* ======================================================================== */
/* logpipe.go - STDIN to UNIX-domain socket log writer                      */
/* Written by Chase Venters <chase.venters@gmail.com>, Public Domain        */
/* ======================================================================== */

package main

import (
	"bufio"
	"flag"
	"log"
	"net"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Define flags
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

// Initialize
func main() {
	flag.Parse()

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

	var plen = len(prefix) + 1

	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(conn)

	// Keep writing data
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
		if !scanner.Scan() {
			break
		}

		// Escape NULLs in output string
		stxt := scanner.Text()
		if *f_esc_null {
			stxt = strings.Replace(stxt, "\x00", "<NUL>", -1);
		}

		if *f_wrap == 0 || len(stxt) + plen < *f_wrap {
			// Queue data for writing
			strout = prefix + stxt + "\n"
		} else {
			// Prepare string builder
			var sb strings.Builder
			sb.WriteString(prefix)
			var lineBytes = plen

			// Wrap stxt, respecting UTF-8 rune boundaries
			for _, runeValue := range stxt {
				var runeBytes = utf8.RuneLen(runeValue)
				if lineBytes + runeBytes > *f_wrap {
					sb.WriteString("\n")
					sb.WriteString(prefix)
					lineBytes = plen
				}
				lineBytes += runeBytes
				sb.WriteRune(runeValue)
			}
			sb.WriteString("\n")

			// Flush string
			strout = sb.String()
		}
	}

	// Reader errors result in immediate exit
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	// On EOF, we just bail...
	os.Exit(0)
}


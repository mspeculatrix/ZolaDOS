// Zolados
// Version: 0.3
// Implements:
//   - File upload (with filename selection)

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stianeikeland/go-rpio"
)

const (
	version        = "0.2"
	strobeDelay    = time.Microsecond * 500 // delay strobing signals
	timeoutDelay   = time.Millisecond * 500 // 100 works
	loadFileOpcode = 128
	saveFileOpcode = 8

	maxFilenameLen = 15

	rescodeErr        = 255
	rescodeMatchState = 1
	rescodeTimeout    = 2
	rescodeTerm       = 4
	fileReadErr       = 64

	ZD_OPCODE_LOAD = 8
	ZD_OPCODE_SAVE = 128
	DIR_INPUT      = 0
	DIR_OUTPUT     = 1
	ACTIVE         = rpio.Low
	NOT_ACTIVE     = rpio.High

	RespOK          = 0
	RespErrOpenFile = 11
	RespErrUnknown  = 12
)

var (
	fileDir   = "/home/pi/zd_files"
	fileName  = "zd.bin"
	clActSig  = rpio.Pin(5)  // PB0
	clRdySig  = rpio.Pin(6)  // PB1
	svrRdySig = rpio.Pin(19) // PB4
	svrActSig = rpio.Pin(16) // PB5
	d0        = rpio.Pin(4)  // PA0..PA7
	d1        = rpio.Pin(17)
	d2        = rpio.Pin(18)
	d3        = rpio.Pin(27)
	d4        = rpio.Pin(22)
	d5        = rpio.Pin(23)
	d6        = rpio.Pin(24)
	d7        = rpio.Pin(25)
	dataPort  = []rpio.Pin{d0, d1, d2, d3, d4, d5, d6, d7}
	dataDirs  = []string{"INPUT", "OUTPUT"}
	verbose   = true
)

func verbosePrintln(msgs ...string) {
	if verbose {
		for _, msg := range msgs {
			fmt.Print(msg + " ")
		}
		fmt.Println()
	}
}

func printLine() {
	verbosePrintln(strings.Repeat("-", 60))
}

func setDataPortDirection(portdir int) {
	verbosePrintln("Setting data port to", dataDirs[portdir])
	for i := 0; i < 8; i++ {
		if portdir == DIR_INPUT {
			dataPort[i].Input()
		} else {
			dataPort[i].Output()
		}
	}
}

func readDataPortValue() int {
	val := 0
	//binary := ""
	for i := 0; i < 8; i++ {
		databit := dataPort[i].Read()
		if databit == rpio.High {
			//		binary = "1" + binary
			val = val | (1 << i)
		} // else {
		//		binary = "0" + binary
		//}
	}
	//verbosePrintln("-", binary)
	//verbosePrintln("- Port value read:", strconv.Itoa(val))
	return val
}

func setDataPortValue(val int) {
	// checkByte := 0
	// binStr := ""
	for i := 0; i < 8; i++ {
		bit := val & (1 << i)
		if bit == 0 {
			// binStr = "0" + binStr
			dataPort[i].Write(rpio.Low)
		} else {
			// binStr = "1" + binStr
			// checkByte = checkByte | (1 << i)
			dataPort[i].Write(rpio.High)
		}
	}
	// fmt.Printf("%s - Data port value set to: 0x%X - checkbyte: 0x%X\n", binStr, val, checkByte)
}

func waitForState(signal rpio.Pin, state rpio.State) int {
	result := rescodeErr
	t := time.Now()
	loop := true
	for loop {
		sigState := signal.Read()
		if sigState == state {
			result = rescodeMatchState
			loop = false
		} else if time.Since(t) >= timeoutDelay {
			result = rescodeTimeout
			loop = false
		}
	}
	return result
}

func serverReadyStrobe() {
	// Take the SR line low to indicate that this server has received
	// whatever signal the client sent, or has placed data on the data bus,
	// and is ready to proceed.
	svrRdySig.Write(ACTIVE)
	time.Sleep(strobeDelay)
	svrRdySig.Write(NOT_ACTIVE)
}

//func writeData() {
//
//}

func main() {
	gpioErr := rpio.Open()
	if gpioErr != nil {
		log.Fatal("Could not open GPIO")
	}

	// command-line flags
	//flag.StringVar(&filepath, "f", filepath, "filename (with full path)")
	flag.BoolVar(&verbose, "v", verbose, "verbose mode")
	flag.Parse()

	verbosePrintln(" ")
	verbosePrintln("ZolaDOS - version", version)
	setDataPortDirection(DIR_INPUT)
	clActSig.Input()
	clRdySig.Input()
	svrRdySig.Output()
	svrActSig.Output()
	svrRdySig.PullUp()
	svrActSig.PullUp()
	svrRdySig.Write(NOT_ACTIVE)
	svrActSig.Write(NOT_ACTIVE)

	standbyLoop := true
	//serverReadyStrobe()
	//reader := bufio.NewReader(os.Stdin)
	verbosePrintln("Main loop")
	printLine()
	verbosePrintln("Waiting for initial CA...")
	for standbyLoop {
		activeState := clActSig.Read() // polling for an /INIT signal from Z64
		if activeState == ACTIVE {
			// --- INITIATE ---
			// The Z64 has initiated a process. We'll want to stay in this
			// block until it is complete.
			verbosePrintln("--- INITIATE ---")
			verbosePrintln("+ CA active")
			result := waitForState(clRdySig, ACTIVE)
			switch result {
			case rescodeMatchState:
				verbosePrintln("+ Received CR - reading code")
				// At this stage, we're expecting to pick up a code from the
				// Z64 indicating what kind of operation it wants to perform.
				opcode := readDataPortValue()
				serverReadyStrobe()
				responseCode := RespOK // default/success
				switch opcode {
				case ZD_OPCODE_LOAD:
					verbosePrintln("--- FILENAME ---")

					// *******************************************************
					//filename := make([]int, 0, maxFilenameLen)
					fileName = ""
					// Wait for CA low
					resperr := waitForState(clActSig, ACTIVE)
					fnloop := true
					for fnloop {
						resperr = waitForState(clRdySig, ACTIVE)
						chrcode := readDataPortValue()
						fileName += string(chrcode)
						serverReadyStrobe()
						caState := clActSig.Read()
						if caState == rpio.High {
							fnloop = false
						}
					}
					// SHOULD DO SOME CHECKS HERE
					fileName += ".BIN"
					verbosePrintln("- Filename:", fileName)
					// *******************************************************

					verbosePrintln("--- SERVER RESPONSE ---")
					readErr := RespOK
					fileOkay := true
					resultStr := "OK"
					resperr = waitForState(clActSig, NOT_ACTIVE)
					if resperr == rescodeTimeout {
						readErr = resperr
						resultStr = "Timed out waiting for CA to be inactive"
					} else {
						setDataPortDirection(DIR_OUTPUT)
						svrActSig.Write(ACTIVE)
						resperr = waitForState(clRdySig, ACTIVE)
						if resperr == rescodeTimeout {
							readErr = resperr
							resultStr = "TO waiting for CR to become active"
						} else {
							filepathname := filepath.Join(fileDir, fileName)
							verbosePrintln("+ Loading file:", filepathname)
							fh, ferr := os.Open(filepathname)
							if ferr != nil {
								verbosePrintln(ferr.Error())
								responseCode = RespErrOpenFile
								fileOkay = false
								resultStr = "Error opening file"
							}
							defer fh.Close()
							// Send response code
							verbosePrintln("+ Sending response code:", fmt.Sprint(responseCode))
							setDataPortValue(responseCode)
							serverReadyStrobe()
							if fileOkay {
								verbosePrintln("--- DATA TRANSFER ---")
								loadLoop := true
								bufferedReader := bufio.NewReader(fh)
								verbosePrintln("+ Loading...")
								/** ---- LOADING LOOP ------------------- */
								for loadLoop {
									dataByte, berr := bufferedReader.ReadByte()
									if berr != nil {
										if berr == io.EOF {
											svrActSig.Write(NOT_ACTIVE)
										} else if berr != io.EOF {
											verbosePrintln(berr.Error())
											readErr = fileReadErr
											resultStr = "Cannot read file"
										}
										loadLoop = false
									} else {
										//fmt.Printf("byte: 0x%X\n", dataByte)
										setDataPortValue(int(dataByte))
										//time.Sleep(time.Millisecond * 2)
										serverReadyStrobe()
										resp1 := waitForState(clRdySig, ACTIVE)
										if resp1 == rescodeTimeout {
											readErr = resp1
											resultStr = "Timed out waiting for CR to be active"
											loadLoop = false
										} else {
											resp2 := waitForState(clRdySig, NOT_ACTIVE)
											if resp2 == rescodeTimeout {
												readErr = resp2
												resultStr = "Timed out waiting for CR to be inactive"
												loadLoop = false
											}
										}
									}
								} // -- end loading loop ---------------------------
								svrActSig.Write(NOT_ACTIVE)
							}
						}
					}
					if readErr > 0 {
						verbosePrintln("*** ERROR:", strconv.Itoa(readErr), resultStr, "***")
					} else {
						verbosePrintln("+ Loading complete:", resultStr)
					}
				case ZD_OPCODE_SAVE:
					verbosePrintln("+ Saving")
				default:
					verbosePrintln("*** Unknown opcode ***")
				}
				svrRdySig.Write(NOT_ACTIVE)
				svrActSig.Write(NOT_ACTIVE)
			case rescodeTerm:
				verbosePrintln("Job done")
			case rescodeTimeout:
				fmt.Println("*** Timed out ***")
			default:
				fmt.Println("Well, this isn't right")
			}
			printLine()
			setDataPortDirection(DIR_INPUT)
			time.Sleep(time.Second)
			// fmt.Print("Press <RETURN> to continue...")
			// key, _ := reader.ReadString('\n')
			verbosePrintln("Waiting for next CA...")
		}
	}
}

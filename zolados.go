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
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stianeikeland/go-rpio"
)

const (
	version         = "0.4"
	strobeDelay     = time.Microsecond * 500 // delay strobing signals
	timeoutDelay    = time.Millisecond * 500 // 100 works
	loadSettleDelay = time.Millisecond * 100 // give load ops a breather
	loadFileOpcode  = 128
	saveFileOpcode  = 8

	stringReadDelay = 500

	maxFilenameLen = 15
	filesPerLine   = 4

	rescodeErr        = 255
	rescodeMatchState = 1
	rescodeTimeout    = 2
	rescodeTerm       = 4
	fileReadErr       = 64

	dataEndCode = 255

	ZD_OPCODE_LOAD = 8
	ZD_OPCODE_LS   = 16
	ZD_OPCODE_SAVE = 128
	DIR_INPUT      = 0
	DIR_OUTPUT     = 1
	ACTIVE         = rpio.Low
	NOT_ACTIVE     = rpio.High
	ONLINE         = rpio.High
	OFFLINE        = rpio.Low

	RespOK          = 0
	RespErrOpenFile = 11
	RespErrLSfail   = 12
	FnameSendErr    = 13 // ??????????
)

var (
	fileDir     = "/home/pi/zd_files"
	fileName    = "ZD"
	clActSig    = rpio.Pin(5)  // PB0
	clRdySig    = rpio.Pin(6)  // PB1
	clOnlineSig = rpio.Pin(12) // PB3
	svrRdySig   = rpio.Pin(19) // PB4
	svrActSig   = rpio.Pin(16) // PB5
	d0          = rpio.Pin(4)  // PA0..PA7
	d1          = rpio.Pin(17)
	d2          = rpio.Pin(18)
	d3          = rpio.Pin(27)
	d4          = rpio.Pin(22)
	d5          = rpio.Pin(23)
	d6          = rpio.Pin(24)
	d7          = rpio.Pin(25)
	dataPort    = []rpio.Pin{d0, d1, d2, d3, d4, d5, d6, d7}
	irq         = rpio.Pin(7)
	intsel      = rpio.Pin(20)
	led         = rpio.Pin(8)
	//dataDirs    = []string{"INPUT", "OUTPUT"}
	verbose     = false
	startTime   time.Time
	elapsedTime time.Duration
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
	verbosePrintln(strings.Repeat("-", 50))
}

func setDataPortDirection(portdir int) {
	//verbosePrintln("- setting data port to", dataDirs[portdir])
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
	for i := 0; i < 8; i++ {
		databit := dataPort[i].Read()
		if databit == rpio.High {
			val = val | (1 << i)
		}
	}
	return val
}

func setDataPortValue(val int) {
	for i := 0; i < 8; i++ {
		bit := val & (1 << i)
		if bit == 0 {
			dataPort[i].Write(rpio.Low)
		} else {
			dataPort[i].Write(rpio.High)
		}
	}
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

func getString() (string, bool, string) {
	gStr := ""
	errFlag := false
	errStr := ""
	//waitForState(clRdySig, NOT_ACTIVE)
	resperr := waitForState(clActSig, ACTIVE) // Wait for CA low
	if resperr == rescodeTimeout {
		errFlag = true
		errStr = "TO waiting for CA in getString() setup"
	} else {
		strloop := true
		for strloop {
			resperr = waitForState(clRdySig, ACTIVE)
			if resperr == rescodeTimeout {
				errFlag = true
				errStr = "TO waiting for CR in getString() loop"
			} else {
				chrcode := readDataPortValue()
				if chrcode > 64 && chrcode < 91 {
					gStr += string(rune(chrcode))
					gStr = strings.TrimSpace(gStr)
				}
				//			fmt.Println(chrcode)
				serverReadyStrobe()
				time.Sleep(time.Microsecond * stringReadDelay)
				caState := clActSig.Read()
				if caState == rpio.High {
					strloop = false
				}

			}
		}
	}
	return gStr, errFlag, errStr
}

func checkClientOnlineState(prevState rpio.State) (rpio.State, bool) {
	changed := false
	state := clOnlineSig.Read()
	if state != prevState {
		changed = true
	}
	return state, changed
}

func sendByte(byteVal int) (int, string) {
	setDataPortValue(byteVal)
	//time.Sleep(time.Millisecond * 2)
	sendErr := 0
	resultStr := ""
	serverReadyStrobe()
	resp1 := waitForState(clRdySig, ACTIVE)
	if resp1 == rescodeTimeout {
		sendErr = resp1
		resultStr = "TO waiting for CR to be active in sendbyte"
	} else {
		resp2 := waitForState(clRdySig, NOT_ACTIVE)
		if resp2 == rescodeTimeout {
			sendErr = resp2
			resultStr = "TO waiting for CR to be inactive in sendbytye"
		}
	}
	return sendErr, resultStr
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
	irq.Output()
	irq.Write(rpio.High)
	intsel.Output()
	intsel.Write(rpio.Low)
	led.Output()
	led.Write(rpio.Low)
	setDataPortDirection(DIR_INPUT)
	clActSig.Input()
	clRdySig.Input()
	clOnlineSig.Input()
	svrRdySig.Output()
	svrActSig.Output()
	svrRdySig.PullUp()
	svrActSig.PullUp()
	svrRdySig.Write(NOT_ACTIVE)
	svrActSig.Write(NOT_ACTIVE)

	standbyLoop := true
	//serverReadyStrobe()
	//reader := bufio.NewReader(os.Stdin)
	clientOnline := OFFLINE
	changed := false
	clientOnlineLastState := OFFLINE
	printLine()
	verbosePrintln("Waiting for initial request...")
	for standbyLoop {
		clientOnline, changed = checkClientOnlineState(clientOnlineLastState)
		if changed {
			clientOnlineLastState = clientOnline
			if clientOnline == ONLINE {
				verbosePrintln("--- ONLINE ---")
			} else {
				verbosePrintln("--- OFFLINE ---")
			}
		}
		if clientOnline == ONLINE {
			activeState := clActSig.Read() // polling for an /INIT signal from Z64
			if activeState == ACTIVE {
				// --- INITIATE ---
				// The Z64 has initiated a process.
				verbosePrintln("+ Request received")
				//verbosePrintln("- CA active")
				result := waitForState(clRdySig, ACTIVE)
				switch result {
				case rescodeMatchState:
					//verbosePrintln("- Received CR - reading code")
					// At this stage, we're expecting to pick up a code from the
					// Z64 indicating what kind of operation it wants to perform.
					opcode := readDataPortValue()
					serverReadyStrobe()
					responseCode := RespOK // default/success
					verbosePrintln("- code read:", strconv.Itoa(opcode))
					switch opcode {
					case ZD_OPCODE_LOAD:
						// *********************************************************
						// ***** LOAD                                            ***
						// *********************************************************
						//verbosePrintln("+ FILENAME")
						okayToContinue := true
						// caStateErr := waitForState(clActSig, NOT_ACTIVE)
						// verbosePrintln("caStateErr:", fmt.Sprint(caStateErr))
						// if caStateErr == rescodeTimeout {
						// 	verbosePrintln("timed out waiting for CA to go high again")
						// }
						fName, errFlag, errStr := getString()
						if !errFlag {
							fileName = fName + ".BIN"
							verbosePrintln("- Filename:", fileName)
						} else {
							verbosePrintln(errStr)
							okayToContinue = false
						}
						if okayToContinue {
							//verbosePrintln("+ SERVER RESPONSE")
							byteCount := 0
							readErr := RespOK
							fileOkay := true
							resultStr := "OK"
							resperr := waitForState(clActSig, NOT_ACTIVE)
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
									verbosePrintln("- Loading file:", filepathname)
									fh, ferr := os.Open(filepathname)
									if ferr != nil {
										verbosePrintln(ferr.Error())
										responseCode = RespErrOpenFile
										fileOkay = false
										resultStr = "Error opening file"
									}
									defer fh.Close()
									// Send response code
									verbosePrintln("- Sending response code:", fmt.Sprint(responseCode))
									setDataPortValue(responseCode)
									serverReadyStrobe()
									svrActSig.Write(NOT_ACTIVE)
									time.Sleep(loadSettleDelay)
									if fileOkay {
										//verbosePrintln("+ DATA TRANSFER")
										loadLoop := true
										bufferedReader := bufio.NewReader(fh)
										verbosePrintln("- Loading...")
										startTime = time.Now()
										svrActSig.Write(ACTIVE)
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
												byteCount++
												//fmt.Printf("0x%X ", dataByte)
												readErr, _ = sendByte(int(dataByte))
												if readErr > 0 {
													loadLoop = false
													resultStr = "Error sending data byte"
												}
											}
										} // -- end loading loop ---------------------------
										svrActSig.Write(NOT_ACTIVE)
										elapsedTime = time.Since(startTime)
									}
								}
							}
							if readErr > 0 {
								verbosePrintln("*** ERROR:", strconv.Itoa(readErr), resultStr, "***")
							} else {
								elapsedTimeStr := ""
								if byteCount > 0 {
									elapsedTimeStr = fmt.Sprintf("- %.3gs", elapsedTime.Seconds())
								}
								verbosePrintln("- Done:", resultStr, "-", strconv.Itoa(byteCount), "bytes", elapsedTimeStr)
							}
						}
					case ZD_OPCODE_LS:
						// *********************************************************
						// ***** LS                                              ***
						// *********************************************************
						result := 0
						verbosePrintln("- List storage")
						svrRdySig.Write(NOT_ACTIVE) // Just to be sure
						svrActSig.Write(ACTIVE)
						//time.Sleep(time.Microsecond * 1000)
						files, lserr := ioutil.ReadDir(fileDir)
						if lserr != nil {
							result = RespErrLSfail
							verbosePrintln("Failed to list files locally", strconv.Itoa(result))
						}
						setDataPortDirection(DIR_OUTPUT)
						for _, file := range files {
							shortName := strings.Split(file.Name(), ".")[0]
							fnLen := len([]rune(shortName))
							fileErr := false
							if fnLen < maxFilenameLen {
								for i := 0; i < fnLen; i++ {
									byteErr, _ := sendByte(int(shortName[i]))
									if byteErr > 0 {
										fileErr = true
									}
								}
								if !fileErr {
									nulErr, _ := sendByte(0)
									if nulErr > 0 {
										fileErr = true
									}
								}
							}
							if fileErr {
								result = FnameSendErr
								break
							}
						}
						// All files sent
						endErr, endStr := sendByte(dataEndCode)
						if endErr != 0 {
							verbosePrintln(endStr)
						}
						// HERE WE SHOULD SEND THE RESULT TO THE PI AS A
						// CONFIRMATION
						svrActSig.Write(NOT_ACTIVE)
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
				setDataPortDirection(DIR_INPUT)
				time.Sleep(time.Millisecond * 100)
				// fmt.Print("Press <RETURN> to continue...")
				// key, _ := reader.ReadString('\n')
				verbosePrintln("- waiting for next request...")
				printLine()
			}
		}
	} // standbyLoop
}

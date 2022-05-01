# ZolaDOS
Go-based file server for the Zolatron 64 6502-based homebrew computer.

https://bit.ly/zolatron64

This uses a Raspberry Pi (a Zero 2W in my case) as a 'disk drive' for the Zolatron.

The Zolatron connects through a 65C22 VIA, using one port as a bidirectional 8-bit parallel data bus, and the other port for unidirectional control signals. Those signals are:

* *CA* Client Active - controlled by the Zolatron.
* *CR* Client Ready - controlled by the Zolatron.
* *SA* Server Active - controlled by this program.
* *SR* Server Ready - controlled by this program.

package multicast

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

type Record struct {
	Name  string
	Type  int
	Class int
	Flag  int
	TTL   int

	Data []byte
}

type MCastPacket struct {
	From *net.UDPAddr

	ID      int
	Flags   int
	qdCount int
	anCount int
	nsCount int
	arCount int

	QDS []*Record
	ANS []*Record
	NSS []*Record
	ARS []*Record
}

func readRec(buf []byte, offset int) (string, int, error) {
	var labels []string
	read := 0
	for {
		if len(buf) <= offset+read {
			return "", 0, errors.New("invalid record: not long enough")
		}
		length := int(buf[offset+read])
		read++
		if length >= 0xc0 {
			// If the top two bits are set, it implies a backreference
			length = p16(slice(buf, offset+read-1, 2)) - 0xc000
			val, _, err := readRec(buf, length)
			if err != nil {
				return "", 0, err
			}

			labels = append(labels, val)
			return strings.Join(labels, "."), read + 1, nil
		}

		if length == 0 {
			break
		}

		val := slice(buf, offset+read, length)
		read += length

		labels = append(labels, string(val))
	}

	return strings.Join(labels, "."), read, nil
}

func slice(buf []byte, offset int, l int) []byte {
	return buf[offset : offset+l]
}

func p16(b []byte) int {
	return int(binary.BigEndian.Uint16(b))
}

func dup(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func parseResourceRecord(buf []byte, offset int, hasData bool) (*Record, int, error) {
	val, n, err := readRec(buf, offset)
	if err != nil {
		return nil, 0, err
	}

	q := new(Record)

	q.Name = val
	q.Type = p16(slice(buf, offset+n, 2))
	c := p16(slice(buf, offset+n+2, 2))
	q.Flag = c & 0x80
	q.Class = c & 0x7f
	n += 4
	if hasData {
		q.TTL = int(binary.BigEndian.Uint32(slice(buf, offset+n, 4)))
		rdatalength := p16(slice(buf, offset+n+4, 2))
		if len(buf) < offset+n+6+rdatalength {
			fmt.Println("bad record name: ", q.Name)
			return nil, 0, fmt.Errorf("rdatalength was longer than the remaining data: %d vs %d", rdatalength, len(buf))
		}
		q.Data = slice(buf, offset+n+6, rdatalength)

		n += int(rdatalength) + 6
	}

	return q, n, nil
}

func ParseMCastPacket(data []byte) (*MCastPacket, error) {
	mcp := new(MCastPacket)
	mcp.ID = int(binary.BigEndian.Uint16(data[0:2]))
	mcp.Flags = int(binary.BigEndian.Uint16(data[2:4]))
	mcp.qdCount = int(binary.BigEndian.Uint16(data[4:6]))
	mcp.anCount = int(binary.BigEndian.Uint16(data[6:8]))
	mcp.nsCount = int(binary.BigEndian.Uint16(data[8:10]))
	mcp.arCount = int(binary.BigEndian.Uint16(data[10:12]))

	offset := 12

	for i := 0; i < mcp.qdCount; i++ {
		rec, n, err := parseResourceRecord(data, offset, false)
		if err != nil {
			return nil, err
		}
		offset += n
		mcp.QDS = append(mcp.QDS, rec)
	}

	for i := 0; i < mcp.anCount; i++ {
		rec, n, err := parseResourceRecord(data, offset, true)
		if err != nil {
			return nil, err
		}
		offset += n
		mcp.ANS = append(mcp.ANS, rec)
	}

	for i := 0; i < mcp.nsCount; i++ {
		rec, n, err := parseResourceRecord(data, offset, true)
		if err != nil {
			return nil, err
		}
		offset += n
		mcp.NSS = append(mcp.NSS, rec)
	}

	for i := 0; i < mcp.arCount; i++ {
		rec, n, err := parseResourceRecord(data, offset, true)
		if err != nil {
			return nil, err
		}
		offset += n
		mcp.ARS = append(mcp.ARS, rec)
	}
	return mcp, nil
}

func GetMulticastPackets() (<-chan *MCastPacket, error) {
	addr, err := net.ResolveUDPAddr("udp", "224.0.0.251:5353")
	if err != nil {
		return nil, err
	}
	c, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	out := make(chan *MCastPacket)

	go func() {
		defer close(out)
		buf := make([]byte, 8192)
		for {
			n, addr, err := c.ReadFromUDP(buf)
			if err != nil {
				fmt.Println("READ ERROR: ", err)
				continue
			}
			if n == 8192 {
				fmt.Println("WARNING!!!! MAX DATA READ!")
			}

			pkt, err := ParseMCastPacket(buf[:n])
			if err != nil {
				fmt.Println("parse error: ", err)
				fmt.Println("bad buffer: ", buf)
				fi, _ := os.Create("BADFILE")
				fi.Write(buf[:n])
				fi.Close()
				os.Exit(1)
				continue
			}
			pkt.From = addr

			out <- pkt
		}
	}()

	return out, nil
}

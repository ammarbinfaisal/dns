package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type Header struct {
	ID uint16 // 16 bits
	// Query/Response indicator
	QR                     byte   // 1 bit
	OpCode                 byte   // 4 bits
	AuthorativeAnswer      byte   // 1 bit
	Truncation             byte   // 1 bit
	RecursionDesired       byte   // 1 bit
	RecursionAvailable     byte   // 1 bit
	Reserved               byte   // 3 bits
	ResponseCode           byte   // 4 bits
	QuestionCount          uint16 // 16 bits
	AnswerRecordCount      uint16 // 16 bits
	AuthorativeRecordCount uint16 // 16 bits
	AdditionalRecordCount  uint16 // 16 bits
}

type Message struct {
	Header     *Header
	Question   []*Question
	Answer     []*Answer
	Authority  byte
	Additional byte
}

type Answer struct {
	Name     string
	Type     uint16
	Class    uint16
	TTL      uint32
	RDLength uint16
	RData    []byte
}

type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

func (h *Header) ToBytes() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[:2], uint16(h.ID))
	buf[2] = h.QR<<7 | h.OpCode<<3 | h.AuthorativeAnswer<<2 | h.Truncation<<1 | h.RecursionDesired
	buf[3] = h.RecursionAvailable<<7 | h.Reserved<<4 | h.ResponseCode
	binary.BigEndian.PutUint16(buf[4:6], h.QuestionCount)
	binary.BigEndian.PutUint16(buf[6:8], h.AnswerRecordCount)
	binary.BigEndian.PutUint16(buf[8:10], h.AuthorativeRecordCount)
	binary.BigEndian.PutUint16(buf[10:12], h.AdditionalRecordCount)
	return buf
}

func (q *Question) ToBytes() []byte {
	labels := strings.Split(q.Name, ".")
	bufsize := 0
	for _, label := range labels {
		bufsize += len(label) + 1
	}
	bufsize += 5
	buf := make([]byte, bufsize)
	copied := 0
	for _, label := range labels {
		buf[copied] = byte(len(label))
		copied++
		copy(buf[copied:], label)
		copied += len(label)
	}
	buf[copied] = 0
	copied++
	binary.BigEndian.PutUint16(buf[copied:copied+2], q.Type)
	binary.BigEndian.PutUint16(buf[copied+2:copied+4], q.Class)
	return buf
}

func (a *Answer) ToBytes() []byte {
	labels := strings.Split(a.Name, ".")
	bufsize := 0
	for _, label := range labels {
		bufsize += len(label) + 1
	}
	bufsize += len(a.RData) + 11
	buf := make([]byte, bufsize)
	copied := 0
	for _, label := range labels {
		buf[copied] = byte(len(label))
		copied++
		copy(buf[copied:], label)
		copied += len(label)
	}
	buf[copied] = 0
	copied++
	binary.BigEndian.PutUint16(buf[copied:copied+2], a.Type)
	binary.BigEndian.PutUint16(buf[copied+2:copied+4], a.Class)
	binary.BigEndian.PutUint32(buf[copied+4:copied+8], a.TTL)
	binary.BigEndian.PutUint16(buf[copied+8:copied+10], a.RDLength)
	copy(buf[copied+10:], a.RData)
	return buf
}

func parseHeader(buf []byte) *Header {
	header := Header{}
	header.ID = binary.BigEndian.Uint16(buf[:2])
	header.QR = buf[2] >> 7
	header.OpCode = buf[2] >> 3 & 0x0F
	header.AuthorativeAnswer = buf[2] >> 2 & 0x01
	header.Truncation = buf[2] >> 1 & 0x01
	header.RecursionDesired = buf[2] & 0x01
	header.RecursionAvailable = buf[3] >> 7
	header.Reserved = buf[3] >> 4 & 0x07
	header.ResponseCode = buf[3] & 0x0F
	header.QuestionCount = binary.BigEndian.Uint16(buf[4:6])
	header.AnswerRecordCount = binary.BigEndian.Uint16(buf[6:8])
	header.AuthorativeRecordCount = binary.BigEndian.Uint16(buf[8:10])
	header.AdditionalRecordCount = binary.BigEndian.Uint16(buf[10:12])
	return &header
}

func parseLabels(buf []byte, start int) ([]string, int) {
	labels := []string{}
	i := start
	for buf[i] != 0 {
		labelLength := int(buf[i])
		if labelLength >= 0xC0 {
			fmt.Printf("pointer: %d\n", i)
			offset := int(binary.BigEndian.Uint16(buf[i:i+2]) & 0x3FFF)
			labels_, _ := parseLabels(buf, offset)
			labels = append(labels, labels_...)
			fmt.Printf("pointer labels: %+v\n", labels)
			return labels, i + 2
		}
		label := string(buf[i+1 : i+1+labelLength])
		labels = append(labels, label)
		i += labelLength + 1
	}
	fmt.Printf("labels: %+v\n", labels)
	return labels, i + 1
}

func parseQuestion(buf []byte, start int) (*Question, int) {
	question := Question{}
	labels, i := parseLabels(buf, start)
	question.Name = strings.Join(labels, ".")
	question.Type = binary.BigEndian.Uint16(buf[i : i+2])
	question.Class = binary.BigEndian.Uint16(buf[i+2 : i+4])
	return &question, i + 4
}

func parseAnswer(buf []byte, ansStart int) *Answer {
	answer := Answer{}
	labels, i := parseLabels(buf, ansStart)
	answer.Name = strings.Join(labels, ".")
	answer.Type = binary.BigEndian.Uint16(buf[i : i+2])
	answer.Class = binary.BigEndian.Uint16(buf[i+2 : i+4])
	answer.TTL = binary.BigEndian.Uint32(buf[i+4 : i+8])
	answer.RDLength = binary.BigEndian.Uint16(buf[i+8 : i+10])
	answer.RData = buf[i+10 : i+10+int(answer.RDLength)]
	return &answer
}

func parseRequest(request []byte) (*Message, error) {
	header := parseHeader(request)
	questions := make([]*Question, 0)
	nextStart := 12
	var question *Question
	answers := make([]*Answer, 0)
	for i := 0; i < int(header.QuestionCount); i++ {
		question, nextStart = parseQuestion(request, nextStart)
		questions = append(questions, question)
	}
	answerCount := int(header.AnswerRecordCount)
	for i := 0; i < answerCount; i++ {
		answers = append(answers, parseAnswer(request, nextStart))
	}
	return &Message{
		Header:   header,
		Question: questions,
		Answer:   answers,
	}, nil
}

func queryDNS(msg *Message, udpConn *net.UDPConn) ([]byte, error) {
	// serialize the message
	buf := make([]byte, 1024*10)
	copied := 0
	copy(buf[copied:], msg.Header.ToBytes())
	copied += 12
	q := msg.Question[0]
	copy(buf[copied:], q.ToBytes())
	copied += len(q.ToBytes())
	_, err := udpConn.Write(buf[:copied])
	if err != nil {
		return nil, err
	}
	n, err := udpConn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func handleConnection(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	// read from udp with buffer

	_, source, err := conn.ReadFromUDP(buf)
	if err != nil {
		fmt.Println("Error receiving data:", err)
		return
	}

	// Create an empty response
	msg, err := parseRequest(buf)
	for _, question := range msg.Question {
		fmt.Printf("question: %+v\n", question)
	}
	if err != nil {
		fmt.Println("Error parsing request:", err)
		return
	}

	// FIXME: this shouldn't be done here
	addressStr := strings.Split(os.Args[1], ":")
	port, err := strconv.Atoi(addressStr[1])
	if err != nil {
		fmt.Println("Error parsing port:", err)
		return
	}

	forwardConn, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.ParseIP(addressStr[0]),
		Port: port,
	})

	if err != nil {
		fmt.Println("Error connecting to DNS server:", err)
		return
	}
	answers := make([]*Answer, 0)
	questions := make([]*Question, 0)

	for _, question := range msg.Question {
		fmt.Printf("question: %+v\n", question)
		header := msg.Header
		header.QuestionCount = 1
		header.AnswerRecordCount = 0
		req := &Message{
			Header:   header,
			Question: []*Question{question},
		}
		resp, err := queryDNS(req, forwardConn)
		if err != nil {
			fmt.Println("Error querying DNS:", err)
			return
		}
		fmt.Printf("resp: %+v\n", resp)
		respMsg, err := parseRequest(resp)
		if err != nil {
			fmt.Println("Error parsing response:", err)
			return
		}
		questions = append(questions, respMsg.Question...)
		answers = append(answers, respMsg.Answer...)
	}
	response := make([]byte, 12)
	msg.Header.QR = 1
	msg.Header.ResponseCode = 0
	if msg.Header.OpCode != 0 {
		msg.Header.ResponseCode = 4
	}
	msg.Header.QuestionCount = uint16(len(questions))
	msg.Header.AnswerRecordCount = uint16(len(answers))
	copy(response, msg.Header.ToBytes())
	copied := 12
	for _, question := range questions {
		response = append(response, question.ToBytes()...)
		copied += len(question.ToBytes())
	}
	for _, answer := range answers {
		fmt.Printf("answer: %+v\n", answer)
		response = append(response, answer.ToBytes()...)
		copied += len(answer.ToBytes())
	}
	fmt.Printf("response: %+v\n", response)

	_, err = conn.WriteToUDP(response, source)
	if err != nil {
		fmt.Println("Failed to send response:", err)
	}
}

func main() {
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:2053")
	if err != nil {
		fmt.Println("Failed to resolve UDP address:", err)
		return
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("Failed to bind to address:", err)
		return
	}
	defer udpConn.Close()

	for {
		handleConnection(udpConn)
	}
}

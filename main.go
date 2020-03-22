package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitly/go-simplejson"
	uuid "github.com/satori/go.uuid"
)

const (
	delimByte = byte(':')
	chunkSize = 10240
	hextable  = "0123456789abcdef"
)

func main() {
	fmt.Println("KaiDeploy by zjyl1994\nhttps://github.com/zjyl1994/kaideploy\n=============")
	if len(os.Args) != 3 {
		fmt.Println("Example: kaideploy localhost:6000 /path/to/kaios/app")
		os.Exit(1)
	}
	debuggerSocket := os.Args[1]
	kaiosAppPath := os.Args[2]
	// package zip in memory
	fmt.Println(">> packing app in zip.")
	packagedAppZip, err := zipToMem(kaiosAppPath)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("ZIP_LENGTH::", len(packagedAppZip))
	fmt.Println(">> zip pack success.")
	// install KaiOS app
	err = installToPhone(debuggerSocket, packagedAppZip)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println(">> all done.")
}

func installToPhone(address string, packagedAppZip []byte) error {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Println("opening debugger socket")
	bufReader := bufio.NewReader(conn)
	// read device info

	_, err = readJSON(bufReader)
	if err != nil {
		return err
	}
	// send listTabs
	sJSON := simplejson.New()
	sJSON.Set("to", "root")
	sJSON.Set("type", "listTabs")
	err = writeJSON(conn, sJSON)
	if err != nil {
		return err
	}
	fmt.Println("listTabs sent")
	// read webappsActor

	sJSON, err = readJSON(bufReader)
	if err != nil {
		return err
	}
	webappsActor := sJSON.Get("webappsActor").MustString()
	fmt.Println("webappsActor:", webappsActor)
	// send uploadPackage
	sJSON = simplejson.New()
	sJSON.Set("to", webappsActor)
	sJSON.Set("type", "uploadPackage")
	err = writeJSON(conn, sJSON)
	if err != nil {
		return err
	}
	fmt.Println("uploadPackage sent")
	// read actor

	sJSON, err = readJSON(bufReader)
	if err != nil {
		return err
	}
	uploadActor := sJSON.Get("actor").MustString()
	fmt.Println("uploadActor:", uploadActor)
	// chunk send
	zipChunk := make([]byte, chunkSize)
	zipReader := bytes.NewReader(packagedAppZip)
	for {
		// send chunk
		n, err := zipReader.Read(zipChunk)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return err
			}
		}
		sJSON = simplejson.New()
		sJSON.Set("to", uploadActor)
		sJSON.Set("type", "chunk")
		sJSON.Set("chunk", jsonEncodeBytes(zipChunk[:n]))
		err = writeJSON(conn, sJSON)
		if err != nil {
			return err
		}
		fmt.Println("chunk sent")
		// get response

		sJSON, err = readJSON(bufReader)
		if err != nil {
			return err
		}
		writtenLen := sJSON.Get("written").MustInt()
		fmt.Println("writtenLen:", writtenLen)
		totalLen := sJSON.Get("_size").MustInt()
		fmt.Println("totalLen:", totalLen)
	}
	// send upload done command
	sJSON = simplejson.New()
	sJSON.Set("to", uploadActor)
	sJSON.Set("type", "done")
	err = writeJSON(conn, sJSON)
	if err != nil {
		return err
	}
	fmt.Println("upload done sent")
	// read resp

	_, err = readJSON(bufReader)
	if err != nil {
		return err
	}
	// send install command
	sJSON = simplejson.New()
	sJSON.Set("to", webappsActor)
	sJSON.Set("upload", uploadActor)
	sJSON.Set("type", "install")
	sJSON.Set("appId", uuid.NewV4().String())
	err = writeJSON(conn, sJSON)
	if err != nil {
		return err
	}
	fmt.Println("install cmd sent")
	// read install resp

	sJSON, err = readJSON(bufReader)
	if err != nil {
		return err
	}
	appId := sJSON.Get("appId").MustString()
	fmt.Println("appId:", appId)
	appPath := sJSON.Get("path").MustString()
	fmt.Println("path:", appPath)
	// remove upload actor
	sJSON = simplejson.New()
	sJSON.Set("to", uploadActor)
	sJSON.Set("type", "remove")
	err = writeJSON(conn, sJSON)
	if err != nil {
		return err
	}
	fmt.Println("remove upload actor command sent")
	return nil
}

func zipToMem(source string) (data []byte, err error) {
	buf := new(bytes.Buffer)
	archive := zip.NewWriter(buf)
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("source not dir")
	}
	filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = strings.TrimPrefix(path, source)
		if info.IsDir() && header.Name == "" {
			return nil
		}
		header.Name = filepath.ToSlash(header.Name)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		fmt.Println(header.Name)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	})
	archive.Close()
	return buf.Bytes(), nil
}

func atoi(s string) int {
	if i, err := strconv.Atoi(s); err == nil {
		return i
	} else {
		return 0
	}
}
func itoa(i int) string {
	return strconv.Itoa(i)
}

func readJSON(r *bufio.Reader) (*simplejson.Json, error) {
	strLen, err := r.ReadString(delimByte)
	if err != nil {
		return nil, err
	}
	strLen = strings.TrimSuffix(strLen, ":")
	bJSON := make([]byte, atoi(strLen))
	_, err = io.ReadFull(r, bJSON)
	if err != nil {
		return nil, err
	}
	//fmt.Println("RESPONSE::", string(bJSON))
	return simplejson.NewJson(bJSON)
}

func writeJSON(w io.Writer, json *simplejson.Json) error {
	bJSON, err := json.MarshalJSON()
	if err != nil {
		return err
	}
	buf := bytes.NewBufferString(itoa(len(bJSON)))
	err = buf.WriteByte(delimByte)
	if err != nil {
		return err
	}
	_, err = buf.Write(bJSON)
	if err != nil {
		return err
	}
	//fmt.Println("REQUEST::", string(buf.Bytes()))
	_, err = w.Write(buf.Bytes())
	return err
}

func jsonEncodeBytes(byteArray []byte) *json.RawMessage {
	var sb strings.Builder
	sb.WriteString(`"`)
	for _, b := range byteArray {
		switch b {
		case 8:
			sb.WriteString(`\b`)
		case 9:
			sb.WriteString(`\t`)
		case 10:
			sb.WriteString(`\n`)
		case 12:
			sb.WriteString(`\f`)
		case 13:
			sb.WriteString(`\r`)
		case 34:
			sb.WriteString(`\"`)
		case 92:
			sb.WriteString(`\\`)
		default:
			if b >= 32 && b <= 126 {
				sb.WriteByte(b)
			} else {
				sb.WriteString(`\u00`)
				sb.WriteByte(hextable[b>>4])
				sb.WriteByte(hextable[b&0x0f])
			}
		}
	}
	sb.WriteString(`"`)
	result := json.RawMessage(sb.String())
	return &result
}
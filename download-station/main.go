package main

import (
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Resp struct {
	Data    Data `json:"data"`
	Success bool `json:"success"`
}
type Data struct {
	Sid string `json:"sid"`
}

type RespList struct {
	Data    DataList `json:"data"`
	Success bool     `json:"success"`
}
type DataList struct {
	Tasks []*Task `json:"tasks"`
}
type Task struct {
	Additional Additional `json:"additional"`
	Status     string     `json:"status"`
	Title      string     `json:"title"`
	Size       int64      `json:"size"`
}
type Additional struct {
	Detail   Detail   `json:"detail"`
	File     []*File  `json:"file"`
	Transfer Transfer `json:"transfer"`
}
type Detail struct {
	CompletedTime int64 `json:"completed_time"`
}

type File struct {
	Filename string `json:"filename"`
}
type Transfer struct {
	SizeUploaded int64 `json:"size_uploaded"`
}

const (
	hosthttp                 = "http://192.168.2.12:5000"
	hostip                   = "192.168.2.12"
	retryNum                 = 2
	batchSize                = 10
	DownloadStationFileLimit = 1000
)

func main() {
	username := os.Getenv("USER")
	passwd := os.Getenv("PASSWD")

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 1 * time.Second,
	}
	authReq, _ := http.NewRequest("GET", hosthttp+"/webapi/auth.cgi", nil)
	authQuery := authReq.URL.Query()
	authQuery.Add("api", "SYNO.API.Auth")
	authQuery.Add("version", "2")
	authQuery.Add("method", "login")
	authQuery.Add("account", username)
	authQuery.Add("passwd", passwd)
	authQuery.Add("session", "DownloadStation")
	authQuery.Add("format", "cookie")
	authReq.URL.RawQuery = authQuery.Encode()
	fmt.Printf("%v %v %v\n", authReq.Method, authReq.URL, authReq.Proto)
	res, err := client.Do(authReq)
	if err != nil {
		fmt.Printf("client.Do(authReq) err: %v", err)
	}
	defer res.Body.Close()
	for _, co := range res.Cookies() {
		fmt.Printf("co is %v\n", co)
	}
	client.Jar.SetCookies(&url.URL{Host: hostip}, res.Cookies())
	b, _ := io.ReadAll(res.Body)
	var resp Resp
	err = json.Unmarshal(b, &resp)
	if err != nil {
		fmt.Printf("json.Unmarshal(b, &resp) err: %v", err)
	}
	if !resp.Success {
		log.Fatalf("auth failed")
		return
	}
	bs, _ := json.Marshal(resp)
	fmt.Println("resp is %v", string(bs))

	MoveNonTaskEntry(client)
}

func MoveNonTaskEntry(client *http.Client) {
	tasks, err := GetTasks(client)
	if err != nil {
		panic(err)
	}
	taskMap := make(map[string]*Task)
	for _, task := range tasks {
		taskMap[task.Title] = task
	}
	log.Infof("taskMap is %v", taskMap)
	// entries under pt folder - task entries = non task entries
	dirEntrys, err := os.ReadDir("/Volumes/hgst8T/pt")
	if err != nil {
		log.Fatalf("os.ReadDir(\"/Volumes/hgst8T\") err: %v", err)
	}
	nonTaskEntries := make([]string, 0)
	for _, dirEntry := range dirEntrys {
		//if i == 10 {
		//	break
		//}
		// filter some entries
		if strings.HasPrefix(dirEntry.Name(), ".") {
			continue
		}
		log.Infof("name is :%s, is_dir: %v", dirEntry.Name(), dirEntry.IsDir())
		if _, ok := taskMap[dirEntry.Name()]; !ok {
			nonTaskEntries = append(nonTaskEntries, dirEntry.Name())
		}
	}
	log.Infof("prepare to move")
	for _, entry := range nonTaskEntries {
		oldPath := filepath.Join("/Volumes/hgst8T/pt", entry)
		newPath := filepath.Join("/Volumes/hgst8T/ptshare", entry)
		log.Infof("moving %s to %s", oldPath, newPath)
		err := os.Rename(oldPath, newPath)
		if err != nil {
			panic(err)
		}

	}

}

func GetTasks(client *http.Client) ([]*Task, error) {
	res := make([]*Task, 0)
	for i := 0; i < DownloadStationFileLimit; i += batchSize {
		tasks, eof, err := GetTasksReq(client, i, batchSize)
		if err != nil {
			return nil, err
		}
		if eof {
			return res, nil
		}
		res = append(res, tasks...)
	}
	return res, nil
}
func GetTasksReq(client *http.Client, offset, limit int) (tasks []*Task, eof bool, err error) {
	log.Infof("requesting offset %v, limit %v", offset, limit)
	res := make([]*Task, 0)
	for i := 0; i < retryNum; i++ {
		listReq, _ := http.NewRequest("GET", hosthttp+"/webapi/DownloadStation/task.cgi", nil)
		listQuery := listReq.URL.Query()
		listQuery.Add("api", "SYNO.DownloadStation.Task")
		listQuery.Add("version", "1")
		listQuery.Add("method", "list")
		listQuery.Add("additional", "detail,file,transfer")
		listQuery.Add("offset", fmt.Sprintf("%d", offset))
		listQuery.Add("limit", fmt.Sprintf("%d", limit))
		listReq.URL.RawQuery = listQuery.Encode()

		res2, err := client.Do(listReq)
		if err != nil {
			log.Errorf("client.Do(listReq) err: %v", err)
			continue
		}
		b2, _ := io.ReadAll(res2.Body)
		_ = res2.Body.Close()
		var resp2 RespList
		err = json.Unmarshal(b2, &resp2)
		if err != nil {
			log.Errorf("json.Unmarshal(b, &resp2) err: %v", err)
			continue
		}
		//bs2, _ := json.Marshal(resp2)
		//log.Infof("resp2 is %v", string(bs2))
		if !resp2.Success {
			return nil, false, fmt.Errorf("success is false")
		}
		log.Infof("getting %d tasks", len(resp2.Data.Tasks))
		if len(resp2.Data.Tasks) == 0 {
			return nil, true, nil
		}
		for _, task := range resp2.Data.Tasks {
			if task.Status == "seeding" || task.Status == "paused" || task.Status == "waiting" || task.Status == "finished" {
				res = append(res, task)
			} else if task.Status == "error" {
				continue
			} else {
				log.Fatalf("task status is %s task title is %s", task.Status, task.Title)
				return nil, false, fmt.Errorf("task status is %s task title is %s", task.Status, task.Title)
			}
		}
		return res, false, nil
	}
	return nil, false, fmt.Errorf("fail after retry")
}

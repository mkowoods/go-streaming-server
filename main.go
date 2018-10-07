package main

/***
https://unix.stackexchange.com/questions/1670/how-can-i-use-ffmpeg-to-split-mpeg-video-into-10-minute-chunks
https://scotch.io/bar-talk/build-a-realtime-chat-server-with-go-and-websockets
http://www.smartjava.org/content/face-detection-using-html5-javascript-webrtc-websockets-jetty-and-javacvopencv
**/
import (
	"bytes"
	"fmt"
	"image/color"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	"gocv.io/x/gocv"
)

const BUFFER_SIZE int = 150 * 1024

//preallocate 512kb of bytes for read
var readBuffer = make([]byte, BUFFER_SIZE)

var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan Message)
var braodcastVideo = make(chan []byte)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		log.Println("request", r)
		return true
	},
	//Use these parameters to controll buffer allocated to messages
	// EnableCompression: true,
	ReadBufferSize:  BUFFER_SIZE,
	WriteBufferSize: BUFFER_SIZE,
}

type Message struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Message  string `json:"message"`
}

func playVideoHandler(w http.ResponseWriter, r *http.Request) {
	header := r.Header
	fmt.Println("Headers", header)

	buf := bytes.NewBuffer(nil)
	f, _ := os.Open("big_buck_bunny.mp4")
	io.Copy(buf, f)
	f.Close()
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Length", "5510872")
	// w.Header().Set("Last-Modified", "Wed, 29 Nov 2017 17:10:44 GMT")
	// w.WriteHeader(206)
	w.Write(buf.Bytes())
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}

	defer ws.Close()
	clients[ws] = true
	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("error: %v", err)
			//if there's an error remove the ws from clients
			delete(clients, ws)
			break
		}
		//push new msg to broadcast channel
		broadcast <- msg
	}
}

func handleFacePrediction(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()
	clients[ws] = true

	//should probably be a global since only needed once per connection
	log.Println("New Classifer generated")
	classifier := gocv.NewCascadeClassifier()
	defer classifier.Close()
	if !classifier.Load("./data/haarcascade_frontalface_default.xml") {
		fmt.Println("Error reading cascade file: data/haarcascade_frontalface_default.xml")
		return
	}
	blue := color.RGBA{0, 0, 255, 0}
	framesSinceLastFace := 0

	for {
		//TODO: need to get the memory overhead under control
		//https://github.com/gorilla/websocket/issues/134

		// _, data, err := ws.ReadMessage()
		_, r, err := ws.NextReader()
		if err != nil {
			log.Printf("error: %v", err)
			delete(clients, ws)
			break
		}

		n, err := r.Read(readBuffer)
		if err != nil {
			log.Printf("error: %v", err)
			return
		}

		log.Println("Read Size", n, len(readBuffer), "Num Clients", len(clients), framesSinceLastFace)

		img, err := gocv.IMDecode(readBuffer[:n], gocv.IMReadColor)
		if err != nil {
			log.Printf("error: %v", err)
			return
		}

		if img.Empty() {
			return
		}

		rects := classifier.DetectMultiScale(img)
		detectedFace := len(rects) > 0

		if detectedFace {
			framesSinceLastFace = 0
		} else {
			framesSinceLastFace++
		}

		if framesSinceLastFace < 15 {
			for _, r := range rects {
				gocv.Rectangle(&img, r, blue, 3)
			}
			imgWithDetection, err := gocv.IMEncode(".png", img)
			if err != nil {
				log.Printf("error: %v", err)
				return
			}
			braodcastVideo <- imgWithDetection
			imgWithDetection = nil
		}

		//clean up data
		//explicitly remove the image to get rid of a memory leak
		img.Close()
		rects = nil
	}
}

func handleMessages() {
	for {
		//read message from broadcast channels
		msg := <-broadcast
		for client := range clients {
			//each client is a websocket connection
			err := client.WriteJSON(msg)
			if err != nil {
				log.Printf("error: %v", err)
				//if there's an error remove the client from clients map
				client.Close()
				delete(clients, client)
				break
			}
		}
	}
}

func handleBroadcastVideo() {
	for {
		img := <-braodcastVideo
		for client := range clients {
			err := client.WriteMessage(websocket.BinaryMessage, img)
			if err != nil {
				log.Printf("error: %v", err)
				client.Close()
				delete(clients, client)
				break
			}
		}
	}
}

func main() {
	assets := http.FileServer(http.Dir("./public"))
	// defer profile.Start(profile.MemProfile).Stop()

	http.Handle("/", assets)
	http.HandleFunc("/playVideo", playVideoHandler)
	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/ws_stream", handleFacePrediction)

	go handleMessages()
	go handleBroadcastVideo()

	log.Println("http server started on :8000")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("ListenAndServer", err)
	}

}

package service

import (
	"encoding/base64"
	"encoding/json"
	"github.com/databrickslabs/databricks-terraform/client/model"
	"log"
	"net/http"
)

// TokensAPI exposes the Secrets API
type DBFSAPI struct {
	Client DBApiClient
}

func (a DBFSAPI) init(client DBApiClient) DBFSAPI {
	a.Client = client
	return a
}

func (a DBFSAPI) Create(path string, overwrite bool, data string) error {
	byteArr, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	byteChunks := split(byteArr, 1e6)
	handle, err := a.createHandle(path, overwrite)
	if err != nil {
		return err
	}
	defer func() {
		err = a.closeHandle(handle)
	}()
	for _, byteChunk := range byteChunks {
		b64Data := base64.StdEncoding.EncodeToString(byteChunk)
		err := a.addBlock(b64Data, handle)
		if err != nil {
			return err
		}
	}
	return err
}

func (a DBFSAPI) Read(path string) (string, error) {
	var bytesFetched []byte
	fetchLoop := true
	offSet := int64(0)
	length := int64(1e6)
	for fetchLoop == true {
		bytesRead, bytes, err := a.read(path, offSet, length)
		if err != nil {
			return "", err
		}
		log.Println(bytesRead)
		if bytesRead == 0 || bytesRead < length {
			fetchLoop = false
		}

		bytesFetched = append(bytesFetched, bytes...)
		offSet = offSet + length
	}
	resp := base64.StdEncoding.EncodeToString(bytesFetched)
	return resp, nil
}

func (a DBFSAPI) Copy(src string, tgt string, client *DBApiClient, overwrite bool) error {
	handle, err := a.createHandle(tgt, overwrite)
	if err != nil {
		return err
	}
	defer func() {
		err = a.closeHandle(handle)
	}()
	fetchLoop := true
	offSet := int64(0)
	length := int64(1e6)
	for fetchLoop == true {
		var api DBFSAPI
		if client == nil {
			api = a
		} else {
			api = client.DBFS()
		}
		bytesRead, b64String, err := api.ReadString(src, offSet, length)
		if err != nil {
			return err
		}
		log.Println(bytesRead)
		if bytesRead == 0 || bytesRead < length {
			fetchLoop = false
		}

		err = a.addBlock(b64String, handle)
		if err != nil {
			return err
		}

		offSet = offSet + length
	}

	return err
}

func (a DBFSAPI) Move(src string, tgt string) error {
	moveRequest := struct {
		SourcePath      string `json:"source_path,omitempty" url:"source_path,omitempty"`
		DestinationPath string `json:"destination_path,omitempty" url:"destination_path,omitempty"`
	}{
		SourcePath:      src,
		DestinationPath: tgt,
	}
	_, err := a.Client.performQuery(http.MethodPost, "/dbfs/move", "2.0", nil, moveRequest)
	return err
}

func (a DBFSAPI) Delete(path string, recursive bool) error {
	deleteRequest := struct {
		Path      string `json:"path,omitempty" url:"path,omitempty"`
		Recursive bool   `json:"recursive,omitempty" url:"recursive,omitempty"`
	}{
		Path:      path,
		Recursive: recursive,
	}
	_, err := a.Client.performQuery(http.MethodPost, "/dbfs/delete", "2.0", nil, deleteRequest)

	return err
}

func (a DBFSAPI) ReadString(path string, offset, length int64) (int64, string, error) {
	var readBytes struct {
		BytesRead int64  `json:"bytes_read,omitempty" url:"bytes_read,omitempty"`
		Data      string `json:"data,omitempty" url:"data,omitempty"`
	}
	readRequest := struct {
		Path   string `json:"path,omitempty" url:"path,omitempty"`
		Offset int64  `json:"offset,omitempty" url:"offset,omitempty"`
		Length int64  `json:"length,omitempty" url:"length,omitempty"`
	}{
		Path:   path,
		Offset: offset,
		Length: length,
	}
	resp, err := a.Client.performQuery(http.MethodGet, "/dbfs/read", "2.0", nil, readRequest)
	if err != nil {
		return readBytes.BytesRead, readBytes.Data, err
	}
	err = json.Unmarshal(resp, &readBytes)

	return readBytes.BytesRead, readBytes.Data, err
}

func (a DBFSAPI) read(path string, offset, length int64) (int64, []byte, error) {
	bytesRead, data, err := a.ReadString(path, offset, length)
	dataBytes, err := base64.StdEncoding.DecodeString(data)
	return bytesRead, dataBytes, err
}

func (a DBFSAPI) Status(path string) (model.FileInfo, error) {
	var fileInfo model.FileInfo
	statusRequest := struct {
		Path string `json:"path,omitempty" url:"path,omitempty"`
	}{
		Path: path,
	}
	resp, err := a.Client.performQuery(http.MethodGet, "/dbfs/get-status", "2.0", nil, statusRequest)
	if err != nil {
		return fileInfo, err
	}
	err = json.Unmarshal(resp, &fileInfo)
	return fileInfo, err
}

func (a DBFSAPI) List(path string, recursive bool) ([]model.FileInfo, error) {
	if recursive == true {
		var paths []model.FileInfo
		a.recursiveAddPaths(path, &paths)
		return paths, nil
	} else {
		return a.list(path)
	}
}

func (a DBFSAPI) recursiveAddPaths(path string, pathList *[]model.FileInfo) {
	fileInfoList, _ := a.list(path)
	for _, v := range fileInfoList {
		if v.IsDir == false {
			*pathList = append(*pathList, v)
		} else if v.IsDir == true {
			a.recursiveAddPaths(v.Path, pathList)
		}
	}
}

func (a DBFSAPI) list(path string) ([]model.FileInfo, error) {
	var dbfsList struct {
		Files []model.FileInfo `json:"files,omitempty" url:"files,omitempty"`
	}
	listRequest := struct {
		Path string `json:"path,omitempty" url:"path,omitempty"`
	}{}
	listRequest.Path = path

	resp, err := a.Client.performQuery(http.MethodGet, "/dbfs/list", "2.0", nil, listRequest)
	if err != nil {
		return dbfsList.Files, err
	}

	err = json.Unmarshal(resp, &dbfsList)
	return dbfsList.Files, err
}

func (a DBFSAPI) Mkdirs(path string) error {
	mkDirsRequest := struct {
		Path string `json:"path,omitempty" url:"path,omitempty"`
	}{}
	mkDirsRequest.Path = path

	_, err := a.Client.performQuery(http.MethodPost, "/dbfs/mkdirs", "2.0", nil, mkDirsRequest)

	return err
}

func (a DBFSAPI) createHandle(path string, overwrite bool) (int64, error) {
	var handle struct {
		Handle int64 `json:"handle,omitempty" url:"handle,omitempty"`
	}
	createDBFSHandleRequest := struct {
		Path      string `json:"path,omitempty" url:"path,omitempty"`
		Overwrite bool   `json:"overwrite,omitempty" url:"overwrite,omitempty"`
	}{
		Path:      path,
		Overwrite: overwrite,
	}

	resp, err := a.Client.performQuery(http.MethodPost, "/dbfs/create", "2.0", nil, createDBFSHandleRequest)
	if err != nil {
		return handle.Handle, err
	}

	err = json.Unmarshal(resp, &handle)
	return handle.Handle, err
}

func (a DBFSAPI) addBlock(data string, handle int64) error {
	var addDBFSBlockRequest = struct {
		Data   string `json:"data,omitempty" url:"data,omitempty"`
		Handle int64  `json:"handle,omitempty" url:"handle,omitempty"`
	}{
		Data:   data,
		Handle: handle,
	}
	_, err := a.Client.performQuery(http.MethodPost, "/dbfs/add-block", "2.0", nil, addDBFSBlockRequest)
	return err
}

func (a DBFSAPI) closeHandle(handle int64) error {
	closeHandleRequest := struct {
		Handle int64 `json:"handle,omitempty" url:"handle,omitempty"`
	}{
		Handle: handle,
	}

	_, err := a.Client.performQuery(http.MethodPost, "/dbfs/close", "2.0", nil, closeHandleRequest)
	return err
}

func split(buf []byte, lim int) [][]byte {
	var chunk []byte
	chunks := make([][]byte, 0, len(buf)/lim+1)
	for len(buf) >= lim {
		chunk, buf = buf[:lim], buf[lim:]
		chunks = append(chunks, chunk)
	}
	if len(buf) > 0 {
		chunks = append(chunks, buf[:len(buf)])
	}
	return chunks
}
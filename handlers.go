// mauImageServer - A self-hosted server to store and easily share images.
// Copyright (C) 2016 Tulir Asokan

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"maunium.net/go/mauimageserver/data"
	log "maunium.net/go/maulogger"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// InsertForm is the form for inserting images into the system. Requirement of AuthToken is configurable.
type InsertForm struct {
	Image       string `json:"image"`
	ImageName   string `json:"image-name"`
	ImageFormat string `json:"image-format"`
	Client      string `json:"client-name"`
	Username    string `json:"username"`
	AuthToken   string `json:"auth-token"`
	Hidden      bool   `json:"hidden"`
}

// DeleteForm is the form for deleting images. AuthToken is required.
type DeleteForm struct {
	ImageName string `json:"image-name"`
	Username  string `json:"username"`
	AuthToken string `json:"auth-token"`
}

// HideForm is the form for hiding/unhiding images. AuthToken is required.
type HideForm struct {
	ImageName string `json:"image-name"`
	Hidden    bool   `json:"hidden"`
	Username  string `json:"username"`
	AuthToken string `json:"auth-token"`
}

// SearchForm is the form for searching for images.
type SearchForm struct {
	Format   string `json:"image-format"`
	Uploader string `json:"uploader"`
	Client   string `json:"client-name"`
	MinTime  int64  `json:"uploaded-after"`
	MaxTime  int64  `json:"uploaded-before"`
}

// String turns a SearchForm into a string
func (sf SearchForm) String() string {
	return fmt.Sprintf("<%[1]s|%[2]s|%[3]s|%[4]d|%[5]d>", sf.Format, sf.Uploader, sf.Client, sf.MinTime, sf.MaxTime)
}

// InsertResponse is the response for an insert call.
type InsertResponse struct {
	Success        bool   `json:"success"`
	Status         string `json:"status-simple"`
	StatusReadable string `json:"status-humanreadable"`
}

func search(w http.ResponseWriter, r *http.Request) {
	var ip = getIP(r)
	if r.Method != "POST" {
		w.Header().Add("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !config.AllowSearch {
		log.Warnf("%[1]s attempted to execute a search, even though it's not allowed", ip)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// Create a json decoder for the payload.
	decoder := json.NewDecoder(r.Body)
	var sf SearchForm
	// Decode the payload.
	err := decoder.Decode(&sf)
	// Check if there was an error decoding.
	if err != nil || (len(sf.Format) == 0 && len(sf.Uploader) == 0 && len(sf.Client) == 0 && sf.MinTime <= 0 && sf.MaxTime <= 0) {
		log.Debugf("%[1]s sent an invalid search request.", ip)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if sf.MinTime <= 0 && sf.MaxTime > 0 {
		sf.MinTime = 1
	} else if sf.MinTime > 0 && sf.MaxTime <= 0 {
		sf.MaxTime = time.Now().Unix()
	}

	results, err := database.Search(sf.Format, sf.Uploader, sf.Client, sf.MinTime, sf.MaxTime)
	if err != nil {
		log.Errorf("Failed to execute search %[2]s by %[1]s: %[3]s", ip, sf.String(), err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Debugf("%[1]s executed a search: %s", ip, sf.String())
	if !output(w, results, http.StatusOK) {
		log.Errorf("Failed to marshal output json to %[1]s: %[2]s", ip, err)
	}
}

func get(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Add("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path[1:]

	img, err := database.Query(path)
	if err == nil {
		date := time.Unix(img.Timestamp, 0).Format(config.DateFormat)
		r.URL.Path = r.URL.Path + "." + img.Format
		data.ImagePage{
			ImageName: img.ImageName,
			ImageAddr: r.URL.String(),
			Uploader:  img.Adder,
			Client:    img.Client,
			Date:      date,
			Index:     strconv.Itoa(img.ID),
		}.Send(w)
		return
	}

	imgData, err := ioutil.ReadFile(config.ImageLocation + r.URL.Path)
	if err != nil {
		log.Errorf("Failed to read image at %[2]s requested by %[1]s: %[3]s", getIP(r), path, err)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)

	split := strings.Split(path, ".")
	if len(split) > 0 {
		img, err = database.Query(split[0])
		if err == nil && len(img.Format) > 0 {
			w.Header().Set("Content-type", "image/"+img.Format)
		} else if len(split) > 1 {
			w.Header().Set("Content-type", "image/"+split[len(split)-1])
		}
	}

	w.Write(imgData)
}

func insert(w http.ResponseWriter, r *http.Request) {
	var ip = getIP(r)
	if r.Method != "POST" {
		w.Header().Add("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Create a json decoder for the payload.
	decoder := json.NewDecoder(r.Body)
	var ifr InsertForm
	// Decode the payload.
	err := decoder.Decode(&ifr)
	// Check if there was an error decoding.
	if err != nil || len(ifr.Image) == 0 {
		log.Debugf("%[1]s sent an invalid insert request.", ip)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Fill out all non-necessary unfilled values.
	if len(ifr.ImageName) == 0 {
		ifr.ImageName = ImageName(5)
	}
	if len(ifr.ImageFormat) == 0 {
		ifr.ImageFormat = "png"
	}
	if len(ifr.Client) == 0 {
		ifr.Client = "Unknown Client"
	}

	if len(ifr.Username) == 0 || len(ifr.AuthToken) == 0 {
		// Username or authentication token not supplied.
		if config.RequireAuth {
			// The user is not logged in, but the config is set to require authentication, send error.
			if !output(w, InsertResponse{
				Success:        false,
				Status:         "not-logged-in",
				StatusReadable: "This MIS server requires authentication. Please log in or register.",
			}, http.StatusUnauthorized) {
				log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, ifr.Username, err)
			}
		} else {
			// The user is not logged in, but login is not required, set username to "anonymous"
			ifr.Username = "anonymous"
		}
	} else {
		// Username and authentication token supplied, check them.
		err = auth.CheckAuthToken(ifr.Username, []byte(ifr.AuthToken))
		if err != nil {
			log.Debugf("%[1]s tried to authenticate as %[2]s with the wrong token.", ip, ifr.Username)
			if !output(w, InsertResponse{
				Success:        false,
				Status:         "invalid-authtoken",
				StatusReadable: "Your authentication token was incorrect. Please try logging in again.",
			}, http.StatusUnauthorized) {
				log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, ifr.Username, err)
			}
			return
		}
	}

	// If the image already exists, make sure that the uploader is the owner of the image.
	var replace = false
	owner := database.GetOwner(ifr.ImageName)
	if len(owner) > 0 {
		if owner != ifr.Username || ifr.Username == "anonymous" {
			if !output(w, InsertResponse{Success: false, Status: "already-exists", StatusReadable: "The requested image name is already in use by another user"}, http.StatusForbidden) {
				log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, ifr.Username, err)
			}
			log.Debugf("%[1]s@%[2]s attempted to override an image uploaded by %[3]s.", ifr.Username, ip, owner)
			return
		}
		replace = true
	}

	// Decode the base64 image from the JSON request.
	image, err := base64.StdEncoding.DecodeString(ifr.Image)
	if err != nil {
		log.Errorf("Error while decoding image from %[1]s@%[2]s: %[3]s", ifr.Username, ip, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	mimeType := http.DetectContentType(image)

	if !strings.HasPrefix(mimeType, "image/") {
		if !output(w, InsertResponse{Success: false, Status: "invalid-mime", StatusReadable: "The uploaded data is of an incorrect MIME type."}, http.StatusUnsupportedMediaType) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, ifr.Username, err)
		}
		return
	}
	mimeType = mimeType[len("image/"):]

	// Write the image to disk.
	err = ioutil.WriteFile(config.ImageLocation+"/"+ifr.ImageName+"."+ifr.ImageFormat, image, 0644)
	if err != nil {
		log.Errorf("Error while saving image from %[1]s@%[2]s: %[3]s", ifr.Username, ip, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !replace {
		// The image name has not been used. Insert it into the database.
		err = database.Insert(ifr.ImageName, ifr.ImageFormat, mimeType, ifr.Username, ip, ifr.Client, ifr.Hidden)
		if err != nil {
			log.Errorf("Error while inserting image from %[1]s@%[2]s into the database: %[3]s", ifr.Username, ip, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Debugf("%[1]s@%[2]s successfully uploaded an image with the name %[3]s (new).", ifr.Username, ip, ifr.ImageName)
		if !output(w, InsertResponse{
			Success:        true,
			Status:         "created",
			StatusReadable: "The image was successfully saved with the name " + ifr.ImageName,
		}, http.StatusCreated) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, ifr.Username, err)
		}
	} else {
		// The image name was in use. Update the data in the database.
		err = database.Update(ifr.ImageName, ifr.ImageFormat, mimeType, ip, ifr.Client, ifr.Hidden)
		if err != nil {
			log.Errorf("Error while updating data of image from %[1]s@%[2]s into the database: %[3]s", ifr.Username, ip, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Debugf("%[1]s@%[2]s successfully uploaded an image with the name %[3]s (replaced).", ifr.Username, ip, ifr.ImageName)
		if !output(w, InsertResponse{
			Success: true,
			Status:  "replaced",
			StatusReadable: "The image was successfully saved with the name " + ifr.ImageName +
				", replacing your previous image with the same name",
		}, http.StatusAccepted) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, ifr.Username, err)
		}
	}
}

func hide(w http.ResponseWriter, r *http.Request) {
	var ip = getIP(r)
	if r.Method != "POST" {
		w.Header().Add("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Create a json decoder for the payload.
	decoder := json.NewDecoder(r.Body)
	var hfr HideForm
	// Decode the payload.
	err := decoder.Decode(&hfr)
	// Check if there was an error decoding.
	if err != nil || len(hfr.ImageName) == 0 || len(hfr.Username) == 0 || len(hfr.AuthToken) == 0 {
		log.Debugf("%[1]s sent an invalid hide request.", ip)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = auth.CheckAuthToken(hfr.Username, []byte(hfr.AuthToken))
	// Check if the auth token was correct
	if err != nil {
		log.Debugf("%[1]s tried to authenticate as %[2]s with the wrong token.", ip, hfr.Username)
		if !output(w, InsertResponse{
			Success:        false,
			Status:         "invalid-authtoken",
			StatusReadable: "The authentication token was incorrect. Please try logging in again.",
		}, http.StatusUnauthorized) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, hfr.Username, err)
		}
		return
	}

	owner := database.GetOwner(hfr.ImageName)
	if len(owner) > 0 {
		if owner != hfr.Username {
			log.Debugf("%[1]s@%[2]s attempted to hide an image uploaded by %[3]s.", hfr.Username, ip, owner)
			if !output(w, InsertResponse{Success: false, Status: "no-permissions", StatusReadable: "The image you requested to be deleted was not uploaded by you."}, http.StatusForbidden) {
				log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, hfr.Username, err)
			}
			return
		}
	} else {
		log.Debugf("%[1]s@%[2]s attempted to hide an image that doesn't exist.", hfr.Username, ip, owner)
		if !output(w, InsertResponse{Success: false, Status: "does-not-exist", StatusReadable: "The image you requested to be deleted does not exist."}, http.StatusNotFound) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, hfr.Username, err)
		}
		return
	}

	err = database.SetHidden(hfr.ImageName, hfr.Hidden)
	if err != nil {
		log.Warnf("Error changing hide status of %[4]s (requested by %[1]s@%[2]s): %[3]s", hfr.Username, ip, err, hfr.ImageName)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var hid string
	if hfr.Hidden {
		hid = "hidden"
	} else {
		hid = "unhidden"
	}

	log.Debugf("%[1]s@%[2]s successfully changed hidden status to %[4]b of the image with the name %[3]s.", hfr.Username, ip, hfr.ImageName, hfr.Hidden)
	if !output(w, InsertResponse{
		Success:        true,
		Status:         hid,
		StatusReadable: "The image " + hfr.ImageName + " was successfully " + hid + ".",
	}, http.StatusAccepted) {
		log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, hfr.Username, err)
	}
}

func delete(w http.ResponseWriter, r *http.Request) {
	var ip = getIP(r)
	if r.Method != "POST" {
		w.Header().Add("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Create a json decoder for the payload.
	decoder := json.NewDecoder(r.Body)
	var dfr DeleteForm
	// Decode the payload.
	err := decoder.Decode(&dfr)
	// Check if there was an error decoding.
	if err != nil || len(dfr.ImageName) == 0 || len(dfr.Username) == 0 || len(dfr.AuthToken) == 0 {
		log.Debugf("%[1]s sent an invalid delete request.", ip)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = auth.CheckAuthToken(dfr.Username, []byte(dfr.AuthToken))
	// Check if the auth token was correct
	if err != nil {
		log.Debugf("%[1]s tried to authenticate as %[2]s with the wrong token.", ip, dfr.Username)
		if !output(w, InsertResponse{
			Success:        false,
			Status:         "invalid-authtoken",
			StatusReadable: "The authentication token was incorrect. Please try logging in again.",
		}, http.StatusUnauthorized) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, dfr.Username, err)
		}
		return
	}

	owner := database.GetOwner(dfr.ImageName)
	if len(owner) > 0 {
		if owner != dfr.Username {
			log.Debugf("%[1]s@%[2]s attempted to delete an image uploaded by %[3]s.", dfr.Username, ip, owner)
			if !output(w, InsertResponse{Success: false, Status: "no-permissions", StatusReadable: "The image you requested to be deleted was not uploaded by you."}, http.StatusForbidden) {
				log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, dfr.Username, err)
			}
			return
		}
	} else {
		log.Debugf("%[1]s@%[2]s attempted to delete an image that doesn't exist.", dfr.Username, ip, owner)
		if !output(w, InsertResponse{Success: false, Status: "does-not-exist", StatusReadable: "The image you requested to be deleted does not exist."}, http.StatusNotFound) {
			log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, dfr.Username, err)
		}
		return
	}

	err = database.Remove(dfr.ImageName)
	if err != nil {
		log.Warnf("Error deleting %[4]s from the database (requested by %[1]s@%[2]s): %[3]s", dfr.Username, ip, err, dfr.ImageName)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	err = os.Remove(config.ImageLocation + "/" + dfr.ImageName + ".png")
	if err != nil {
		// If the file just didn't exist, warn about the error. If the error was something else, cancel.
		if strings.HasSuffix(err.Error(), "no such file or directory") {
			log.Warnf("Error deleting %[3]s from the filesystem (requested by %[1]s@%[2]s): File not found", dfr.Username, ip, dfr.ImageName)
		} else {
			log.Errorf("Error deleting %[4]s from the filesystem (requested by %[1]s@%[2]s): %[3]s", dfr.Username, ip, err, dfr.ImageName)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	log.Debugf("%[1]s@%[2]s successfully deleted the image with the name %[3]s.", dfr.Username, ip, dfr.ImageName)
	if !output(w, InsertResponse{
		Success:        true,
		Status:         "deleted",
		StatusReadable: "The image " + dfr.ImageName + " was successfully deleted.",
	}, http.StatusAccepted) {
		log.Errorf("Failed to marshal output json to %[1]s@%[2]s: %[3]s", ip, dfr.Username, err)
	}
}

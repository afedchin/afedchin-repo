package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bradfitz/slice"
	"github.com/gorilla/mux"
	"github.com/mcuadros/go-version"
)

var repositories = []string{
	"afedchin/xbmctorrent",
	"afedchin/antizapret",
}

var indexTemplate, _ = template.New("index").Parse(
	`<html>
    <head><title>Index</title></head>
    <body>
        <ul>
           {{ range $k, $v := . }}<li><a href="{{$k}}/{{$v}}">{{$v}}</a></li>
           {{ end }}
        </ul>
    </body>
</html>`)

type Content struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Sha         string `json:"sha"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
	HTMLURL     string `json:"html_url"`
	GitURL      string `json:"git_url"`
	DownloadURL string `json:"download_url"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
}

type ReleaseAsset struct {
	URL           string `json:"url"`
	DownloadURL   string `json:"browser_download_url"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Label         string `json:"label"`
	ContentType   string `json:"content_type"`
	State         string `json:"state"`
	Size          int64  `json:"size"`
	DownloadCount int64  `json:"download_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"published_at"`
}

type Release struct {
	URL             string         `json:"url"`
	HTMLURL         string         `json:"html_url"`
	AssetsURL       string         `json:"assets_url"`
	UploadURL       string         `json:"upload_url"`
	ID              int64          `json:"id"`
	TagName         string         `json:"tag_name"`
	TargetCommitish string         `json:"target_commitish"`
	Name            string         `json:"name"`
	Body            string         `json:"body"`
	Draft           string         `json:"draft"`
	Prerelease      bool           `json:"prerelease"`
	CreatedAt       time.Time      `json:"created_at"`
	PublishedAt     time.Time      `json:"published_at"`
	Assets          []ReleaseAsset `json:"assets"`
}

type KodiAddon struct {
	ID       string `xml:"id,attr"`
	Version  string `xml:"version,attr"`
	XMLBody  string
	Releases []Release
}

var addons map[string]KodiAddon

func reloadAddons(repos []string) map[string]KodiAddon {
	tmpAddons := map[string]KodiAddon{}
	for _, repo := range repos {
		releaseURL := fmt.Sprintf("https://api.github.com/repos/%s/releases", repo)
		response, err := http.Get(releaseURL)
		if err != nil {
			return nil
		}
		body, _ := ioutil.ReadAll(response.Body)
		var releases []Release
		json.Unmarshal(body, &releases)

		slice.Sort(releases[:], func(i, j int) bool {
			return version.Compare(releases[i].TagName, releases[j].TagName, ">")
		})

		var addonxml Content
		var addon KodiAddon

		lastRelease := releases[0]
		addonURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/addon.xml.tpl?ref=%s", repo, lastRelease.TagName)
		response, err = http.Get(addonURL)
		if err != nil {
			return nil
		}
		body, _ = ioutil.ReadAll(response.Body)
		json.Unmarshal(body, &addonxml)

		decoded, err := base64.StdEncoding.DecodeString(addonxml.Content)
		if err != nil {
			return nil
		}

		xmlBody := ""
		scanner := bufio.NewScanner(bytes.NewBuffer(decoded))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "<?xml") == true {
				continue
			}
			if strings.Contains(line, "$VERSION") == true {
				line = strings.Replace(line, "$VERSION", lastRelease.TagName[1:], 1)
			}
			xmlBody = fmt.Sprintf("%s\r\n%s", xmlBody, line)
		}
		var buf bytes.Buffer
		buf.WriteString(xmlBody)
		xml.Unmarshal(buf.Bytes(), &addon)

		addon.XMLBody = fmt.Sprintf("%s\r\n", xmlBody)
		addon.Releases = releases

		tmpAddons[addon.ID] = addon
	}
	return tmpAddons
}

func repoMuxer(repos []string) *mux.Router {
	router := mux.NewRouter()

	keys := []string{}
	addons = reloadAddons(repos)
	for key := range addons {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		files := make(map[string]string)
		for _, addonID := range keys {
			for _, asset := range addons[addonID].Releases[0].Assets {
				if strings.HasSuffix(asset.Name, ".zip") {
					files[addonID] = asset.Name
				}
			}
		}
		indexTemplate.Execute(w, files)
	})

	router.HandleFunc("/addons.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<addons>")
		for _, addonID := range keys {
			fmt.Fprintf(w, addons[addonID].XMLBody)
		}
		fmt.Fprintf(w, "</addons>")
	})

	router.HandleFunc("/addons.xml.md5", func(w http.ResponseWriter, r *http.Request) {
		h := md5.New()
		fmt.Fprintf(h, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<addons>")
		for _, addonID := range keys {
			fmt.Fprintf(h, addons[addonID].XMLBody)
		}
		fmt.Fprintf(h, "</addons>")
		fmt.Fprintf(w, "%x", h.Sum(nil))
	})

	router.HandleFunc("/{addon_id}/changelog-{version}.txt", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if addon, ok := addons[vars["addon_id"]]; ok {
			for _, release := range addon.Releases {
				fmt.Fprintf(w, "%s\n-------\n%s\n\n", release.TagName, release.Body)
			}
		}
	})

	router.HandleFunc("/{addon_id}/fanart.jpg", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if addon, ok := addons[vars["addon_id"]]; ok {
			for _, asset := range addon.Releases[0].Assets {
				if strings.Compare(asset.Name, "fanart.jpg") == 0 {
					http.Redirect(w, r, asset.DownloadURL, 302)
				}
			}
		}
	})

	router.HandleFunc("/{addon_id}/icon.png", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if addon, ok := addons[vars["addon_id"]]; ok {
			for _, asset := range addon.Releases[0].Assets {
				if strings.Compare(asset.Name, "icon.png") == 0 {
					http.Redirect(w, r, asset.DownloadURL, 302)
				}
			}
		}
	})

	router.HandleFunc("/{addon_id}/{file}.zip", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if addon, ok := addons[vars["addon_id"]]; ok {
			file := fmt.Sprintf("%s.zip", vars["file"])
			for _, asset := range addon.Releases[0].Assets {
				if strings.Compare(asset.Name, file) == 0 {
					http.Redirect(w, r, asset.DownloadURL, 302)
				}
			}
		}
	})

	router.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		keys = []string{}
		addons = reloadAddons(repos)
		for key := range addons {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	})

	return router
}

func main() {
	http.Handle("/", repoMuxer(repositories))
	fmt.Println("listening...")
	err := http.ListenAndServe("0.0.0.0:"+os.Getenv("PORT"), nil)
	if err != nil {
		panic(err)
	}
}

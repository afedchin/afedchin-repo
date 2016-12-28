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
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bradfitz/slice"
	"github.com/gorilla/mux"
	"github.com/mcuadros/go-version"
)

var repositories = []string{"afedchin/xbmctorrent", "afedchin/antizapret"}

var IndexTemplate, _ = template.New("index").Parse(
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

type XBMCAddon struct {
	ID       string `xml:"id,attr"`
	Version  string `xml:"version,attr"`
	XMLBody  string
	Releases []Release
}

var addons map[string]XBMCAddon

func reloadAddons(repos []string) map[string]XBMCAddon {
	tmpAddons := map[string]XBMCAddon{}
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
			return version.Compare(releases[i].Name, releases[j].Name, ">")
		})

		var addonxml Content
		var addon XBMCAddon

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

func assertError(err error) {
	if err != nil {
		os.Exit(1)
	}
}

func createIfNotExist(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.MkdirAll(dir, os.ModePerm)
	}
}

func overWriteFile(filePath string) *os.File {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.ModePerm)
	assertError(err)
	return file
}

func generateRepo(repos []string, baseDir string) {
	addons = reloadAddons(repos)

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		os.MkdirAll(baseDir, os.ModePerm)
	}

	files := make(map[string]string)
	for _, addon := range addons {
		addonDir := path.Join(baseDir, addon.ID)
		for _, asset := range addon.Releases[0].Assets {
			if strings.HasSuffix(asset.Name, addon.Version[1:]+".zip") {
				files[addon.ID] = asset.Name

				filepath := path.Join(addonDir, asset.Name)
				// ignore exists files
				if _, err := os.Stat(filepath); err == nil {
					continue
				}
				createIfNotExist(addonDir)
				file := overWriteFile(filepath)

				response, err := http.Get(asset.DownloadURL)
				assertError(err)

				_, err = io.Copy(file, response.Body)
				assertError(err)

				file.Close()
				response.Body.Close()
			}
		}
		addonxml := path.Join(addonDir, "addon.xml")
		file := overWriteFile(addonxml)
		file.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>")
		file.WriteString(addon.XMLBody)
		file.Close()
	}

	f := overWriteFile(path.Join(baseDir, "index.html"))
	IndexTemplate.Execute(f, files)
	f.Close()

	f = overWriteFile(path.Join(baseDir, "addons.xml"))

	h := md5.New()
	fmt.Fprintf(f, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<addons>")
	fmt.Fprintf(h, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<addons>")
	for _, addon := range addons {
		fmt.Fprintf(f, addon.XMLBody)
		fmt.Fprintf(h, addon.XMLBody)
	}
	fmt.Fprintf(f, "</addons>")
	fmt.Fprintf(h, "</addons>")
	f.Close()

	f = overWriteFile(path.Join(baseDir, "addons.xml.md5"))
	fmt.Fprintf(f, "%x", h.Sum(nil))
	f.Close()
}

func repoMuxer(repos []string) *mux.Router {
	router := mux.NewRouter()
	addons = reloadAddons(repos)

	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		files := make(map[string]string)
		for _, addon := range addons {
			for _, asset := range addon.Releases[0].Assets {
				if strings.HasSuffix(asset.Name, ".zip") {
					files[addon.ID] = asset.Name
				}
			}
		}
		IndexTemplate.Execute(w, files)
	})

	router.HandleFunc("/addons.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<addons>")
		for _, addon := range addons {
			fmt.Fprintf(w, addon.XMLBody)
		}
		fmt.Fprintf(w, "</addons>")
	})

	router.HandleFunc("/addons.xml.md5", func(w http.ResponseWriter, r *http.Request) {
		h := md5.New()
		fmt.Fprintf(h, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<addons>")
		for _, addon := range addons {
			fmt.Fprintf(h, addon.XMLBody)
		}
		fmt.Fprintf(h, "</addons>")
		fmt.Fprintf(w, "%x", h.Sum(nil))
	})

	router.HandleFunc("/{addon_id}/changelog-{version}.txt", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		addon := addons[vars["addon_id"]]
		for _, release := range addon.Releases {
			fmt.Fprintf(w, "%s\n-------\n%s\n\n", release.Name, release.Body)
		}
	})

	/*router.HandleFunc("/{addon_id}/fanart.jpg", func(w http.ResponseWriter, r *http.Request) {
	      vars := mux.Vars(r)
	      addon := addons[vars["addon_id"]]
	      http.Redirect(w, r, addon.Releases[0].AssetDownloadURL("fanart.jpg"), 302)
	  })

	  router.HandleFunc("/{addon_id}/icon.png", func(w http.ResponseWriter, r *http.Request) {
	      vars := mux.Vars(r)
	      addon := addons[vars["addon_id"]]
	      http.Redirect(w, r, addon.Releases[0].AssetDownloadURL("icon.png"), 302)
	  })*/

	router.HandleFunc("/{addon_id}/{file}.zip", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		addon := addons[vars["addon_id"]]
		file := fmt.Sprintf("%s.zip", vars["file"])
		for _, asset := range addon.Releases[0].Assets {
			if strings.Compare(asset.Name, file) == 0 {
				http.Redirect(w, r, asset.DownloadURL, 302)
			}
		}
	})

	router.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		addons = reloadAddons(repos)
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

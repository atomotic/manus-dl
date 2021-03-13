package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/cheggaaa/pb/v3"
	"github.com/gammazero/workerpool"
)

const (
	base         string = "https://manus.iccu.sbn.it"
	fondslist    string = "opac_ElencoSchedeDiUnFondo.php"
	itempage     string = "opac_SchedaScheda.php"
	interstitial string = "Backoffice/XML/index_immediato.php"
	download     string = "Backoffice/XML/dl.php"
)

var (
	fonds = flag.Int("fonds-id", 0, "fonds identifier")
)

func unique(s []string) []string {
	unique := make(map[string]bool, len(s))
	us := make([]string, len(unique))
	for _, elem := range s {
		if len(elem) != 0 {
			if !unique[elem] {
				us = append(us, elem)
				unique[elem] = true
			}
		}
	}
	return us
}

// getPages parse a fonds page to get pagination and items numbers
// example url: https://manus.iccu.sbn.it/opac_ElencoSchedeDiUnFondo.php?ID=485
func getPages(url string) (pages int, items int, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)

	regexp, _ := regexp.Compile(`Pagina (\d+) di (\d+\.?\d*) \(occorrenze (\d+)\)`)
	results := regexp.FindStringSubmatch(string(body))

	if len(results) > 0 {
		pages, _ := strconv.Atoi(results[2])
		items, _ := strconv.Atoi(results[3])
		return pages, items, nil
	}

	return 0, 0, errors.New("no results")
}

// getIds returns a unique lists of identifiers for each page of a fonds
// example url: https://manus.iccu.sbn.it/opac_ElencoSchedeDiUnFondo.php?ID=485&page=2
func getIds(url string) (ids []string) {
	res, err := http.Get(url)
	if err != nil {
		log.Println(err)
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Println(err)
	}

	doc.Find("a.opac_linkNero").Each(func(i int, s *goquery.Selection) {
		ix, _ := s.Attr("href")
		id := strings.Split(ix, "=")
		ids = append(ids, id[1])
	})

	return unique(ids)
}

// downloadXML saves a TEI xml file of an item
func downloadXML(item string, bar *pb.ProgressBar) func() {
	return func() {

		input := fmt.Sprintf("%s/%s?ID=%s", base, itempage, item)

		res, err := http.Get(input)
		if err != nil {
			log.Println(err)
			return
		}
		defer res.Body.Close()

		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			log.Println(err)
			return
		}

		filename, _ := doc.Find("input[name=filename]").First().Attr("value")
		autore, _ := doc.Find("input[name=autore]").First().Attr("value")
		if filename != "" {
			res, err = http.PostForm(fmt.Sprintf("%s/%s", base, interstitial),
				url.Values{"op": {"manos"},
					"cnmdManos": {item},
					"autore":    {autore},
					"filename":  {filename},
				})

			if err != nil {
				log.Println(err)
				return
			}
			defer res.Body.Close()

			out, err := os.Create(fmt.Sprintf("%s/%s", "./manus-data", filename))
			if err != nil {
				log.Println(err)
				return
			}
			defer out.Close()

			_, err = io.Copy(out, res.Body)
			if err != nil {
				log.Println(err)
				return
			}
		} else {
			log.Printf("error on item: %s", item)
		}

		bar.Increment()
	}
}

func main() {
	wp := workerpool.New(8)

	// use insecure https - to test with mimtproxy
	// http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	flag.Parse()
	if *fonds == 0 {
		flag.PrintDefaults()
		os.Exit(0)
	}

	if _, err := os.Stat("./manus-data"); os.IsNotExist(err) {
		os.Mkdir("./manus-data", 0755)
	}

	fondo := fmt.Sprintf("%s/%s?ID=%d", base, fondslist, *fonds)
	p, i, err := getPages(fondo)
	if err != nil {
		log.Println(err)
		os.Exit(0)
	}

	fmt.Printf("# fonds: %d — pages: %d — items: %d\n", *fonds, p, i)

	bar := pb.StartNew(i)

	for i := 0; i < p; i++ {
		p0 := fmt.Sprintf("%s/%s?ID=%d&page=%d", base, fondslist, *fonds, i)
		ids := getIds(p0)
		for _, item := range ids {
			wp.Submit(downloadXML(item, bar))
		}
	}

	wp.StopWait()
	bar.Finish()

}

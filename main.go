package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/gookit/color"
	"github.com/mitchellh/mapstructure"
	flag "github.com/spf13/pflag"
	"gopkg.in/yaml.v2"
)

// flags
var apiUrl string
var dest string
var saveMeta bool
var prefix string
var logFile string
var wpUploads string
var silent bool
var quiet bool
var sample int
var postFilename string
var frontmatterFile string
var showHelp bool
var showVersion bool

const myName = "wordpress-export"
const version = "0.1"

func init() {
	flag.BoolVar(&saveMeta, "meta", false, "save tags, categories and authors")
	flag.StringVar(&apiUrl, "api", "", "Base URL of the WordPress API")
	flag.StringVarP(&dest, "output", "o", "./output", "Save results to this directory")
	flag.StringVar(&prefix, "prefix", "", "Strip this prefix off post paths")
	flag.StringVar(&logFile, "log", "", "Log progress to this file")
	flag.StringVar(&wpUploads, "assets", "/wp-content/uploads/", "Copy assets under this path")
	flag.BoolVarP(&quiet, "quiet", "q", false, "Don't print progress")
	flag.BoolVar(&silent, "silent", false, "Don't print progress or warnings")
	flag.IntVar(&sample, "sample", 0, "Only retrieve this many posts")
	flag.StringVar(&postFilename, "postfile", "index.md", "The filename for each post")
	flag.StringVar(&frontmatterFile, "frontmatter", "", "Read additional frontmatter from this file")

	flag.BoolVarP(&showHelp, "help", "h", false, "Show this help")
	flag.BoolVarP(&showVersion, "version", "V", false, "Show version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <your blog url>\n", myName)
		flag.PrintDefaults()
	}
}

var logWriter io.Writer

func main() {
	flag.Parse()
	if showHelp {
		flag.Usage()
		os.Exit(0)
	}
	if showVersion {
		fmt.Fprintf(os.Stderr, "%s version %s\n", myName, version)
		os.Exit(0)
	}

	// Set up log file and verbosity
	var err error
	if logFile != "" {
		logWriter, err = os.Create(logFile)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to open log file %s: %v\n", logFile, err)
			os.Exit(1)
		}
	}

	quiet = quiet || silent

	// Handle the parameter, which we hope is "a link to the site"
	switch flag.NArg() {
	default:
		fatal("%s takes only one parameter, the url of the wordpress site", myName)
	case 0:
	case 1:
		siteUrl, err := parseURL(flag.Arg(0))
		if err != nil {
			fatal("'%s' doesn't look like a url: %v", flag.Arg(0), err)
		}
		if apiUrl == "" {
			apiUrl = findApi(siteUrl)
		}
	}

	if apiUrl == "" {
		fatal("I couldn't find the API of the site to export, try with '%s <url>' or with --api", myName)
	}

	if !strings.HasSuffix(apiUrl, "/") {
		apiUrl = apiUrl + "/"
	}

	frontmatter := ""
	if frontmatterFile != "" {
		file, err := os.Open(frontmatterFile)
		if err != nil {
			fatal("Failed to open '%s': %v", frontmatterFile, err)
		}
		var buff bytes.Buffer
		_, err = buff.ReadFrom(file)
		if err != nil {
			fatal("Failed to read from '%s': %v", frontmatterFile, err)
		}
		frontmatter = strings.TrimSpace(buff.String()) + "\n"
	}

	_ = os.MkdirAll(dest, 0755)

	info("Using API at %s", apiUrl)
	users := getUsers()
	categories := getCategories()
	tags := getTags()
	if saveMeta {
		writeMeta("users", &users)
		writeMeta("categories", &categories)
		writeMeta("tags", &tags)
	}
	posts := getPosts()

	for _, p := range posts {
		author, ok := users[p.Author]
		if !ok {
			fatal("No such author as %d in post %s", p.Author, p.Link)
		}
		p.AuthorName = author.Name

		catNames := []string{}
		for _, category := range p.Categories {
			cat, ok := categories[category]
			if !ok {
				fatal("No such category as %d in post %s", category, p.Link)
			}
			catNames = append(catNames, cat.Name)
		}
		p.CategoryNames = catNames

		tagNames := []string{}
		for _, tag := range p.Tags {
			t, ok := tags[tag]
			if !ok {
				fatal("No such tag as %d in post %s", tag, p.Link)
			}
			tagNames = append(tagNames, t.Name)
		}
		p.TagNames = tagNames
		savePost(p, frontmatter)
	}
	status("Saved all posts")
}

type ResultPost struct {
	Template   string   `yaml:"template"`
	Title      string   `yaml:"title"`
	Date       string   `yaml:"date"`
	Excerpt    string   `yaml:"excerpt"`
	Author     string   `yaml:"author"`
	Categories []string `yaml:"categories"`
	Tags       []string `yaml:"tags"`
	Body       string   `yaml:"-"`
}

// Save metadata as json
func writeMeta(name string, data interface{}) {
	filename := filepath.Join(dest, name+".json")
	of, err := os.Create(filename)
	if err != nil {
		fatal("Failed to create %s: %v", filename, err)
	}
	encoder := json.NewEncoder(of)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(data)
	if err != nil {
		fatal("Failed to marshal to %s: %v", filename, err)
	}
}

// Intuit the (http) path from the link of the post
func postDirectory(p Post) []string {
	dir := ""
	if prefix != "" && strings.HasPrefix(p.Link, prefix) {
		dir = strings.TrimPrefix(p.Link, prefix)
	} else {
		u, err := url.Parse(p.Link)
		if err != nil {
			fatal("failed to parse url of post '%s': %v", p.Link, err)
		}
		dir = strings.TrimPrefix(u.Path, prefix)
	}
	return strings.FieldsFunc(dir, func(c rune) bool { return c == '/' })
}

func savePost(p Post, frontmatter string) {
	sourceUrl, err := url.Parse(p.Link)
	if err != nil {
		fatal("Failed to parse post url '%s': %v", p.Link, err)
	}
	if !sourceUrl.IsAbs() {
		fatal("Post URL '%s' isn't absolute", p.Link)
	}
	status("Processing %s", sourceUrl.Path)
	_, err = time.Parse("2006-01-02T15:04:05", p.DateGmt)
	if err != nil {
		warn("Failed to parse date for %s '%s': %v", p.Link, p.DateGmt, err)
	}
	postPath := postDirectory(p)

	// Where do we write the output for this post?
	outputDir := filepath.Join(append([]string{dest}, postPath...)...)
	err = os.MkdirAll(outputDir, 0755)
	if err != nil {
		fatal("Failed to create directory %s: %v", outputDir, err)
	}
	outputFile := filepath.Join(outputDir, postFilename)
	of, err := os.Create(outputFile)
	if err != nil {
		fatal("Failed to create file %s: %v", outputFile, err)
	}

	post := ResultPost{
		Template:   "blog-post",
		Title:      p.Title.Rendered,
		Date:       p.DateGmt,
		Excerpt:    p.Excerpt.Rendered,
		Author:     p.AuthorName,
		Categories: p.CategoryNames,
		Tags:       p.TagNames,
	}

	// Parse the rendered content of the post
	tree, err := html.Parse(bytes.NewReader([]byte(p.Content.Rendered)))
	if err != nil {
		fatal("Couldn't parse html for %s: %v", p.Link, err)
	}

	// Write the YAML frontmatter
	_, _ = of.WriteString("---\n")
	enc := yaml.NewEncoder(of)
	err = enc.Encode(post)
	if err != nil {
		fatal("Failed to encode frontmatter for %s: %v", p.Link, err)
	}
	err = enc.Close()
	if err != nil {
		fatal("Failed to close frontmatter for %s: %v", p.Link, err)
	}
	_, _ = of.WriteString(frontmatter)
	_, _ = of.WriteString("---\n")

	fixInternalLinks(tree, outputDir, sourceUrl)
	fixImages(tree, outputDir, sourceUrl)
	renderBody(p.Link, tree, of)
}

func renderBody(name string, root *html.Node, w io.Writer) {
	bodyNode := findBody(root)
	if bodyNode == nil {
		fatal("Failed to find body in %s", name)
		panic("not reached")
	}
	child := bodyNode.FirstChild
	for child != nil {
		err := html.Render(w, child)
		if err != nil {
			fatal("Failed to render body in %s: %v", name, err)
		}
		child = child.NextSibling
	}
}

type Asset struct {
	Url      *url.URL
	Filename string
}

// Return a remote URL and local filename for assets that we want
// to copy. We assume that any asset on the same hostname is worth
// considering as a local asset or page.
func localAsset(assetUrl string, sourceUrl *url.URL) *Asset {
	au, err := url.Parse(strings.TrimSpace(assetUrl))
	if err != nil {
		warn("Failed to parse asset url '%s': %v", assetUrl, err)
		return nil
	}

	refUrl := sourceUrl.ResolveReference(au)
	assetFilename := path.Base(refUrl.Path)
	if strings.ToLower(sourceUrl.Host) == strings.ToLower(refUrl.Host) {
		return &Asset{
			Url:      refUrl,
			Filename: assetFilename,
		}
	}
	return nil
}

var plausibleSuffixRe = regexp.MustCompile(`\.(png|jpg|gif|pdf|jpeg)$`)

func fetchAsset(asset *Asset, dir string) string {
	if !strings.HasPrefix(strings.ToLower(asset.Url.Path), wpUploads) {
		// internal link to a page, so don't mirror it
		return asset.Url.Path
	}
	if !plausibleSuffixRe.MatchString(asset.Filename) {
		warn("Suspicious filename: %s", asset.Filename)
	}
	resp, err := http.Get(asset.Url.String())
	if err != nil {
		warn("Failed to get linked file %s: %v", asset.Url, err)
		return asset.Url.String()
	} else {
		if resp.StatusCode != 200 {
			warn("Non-200 response fetching file: %s (%s)", asset.Url, resp.Status)
			return asset.Url.String()
		} else {
			of, err := os.Create(filepath.Join(dir, asset.Filename))
			if err != nil {
				fatal("Failed to write %s to %s: %v", asset.Filename, dir, err)
			}
			_, err = io.Copy(of, resp.Body)
			if err != nil {
				fatal("Failed to copy %s to %s/%s: %v", asset.Url, dir, asset.Filename, err)
			}
			return asset.Filename
		}
	}
}

func fixInternalLinks(node *html.Node, dir string, sourceUrl *url.URL) {
	if node.Type == html.ElementNode && node.Data == "a" {
		for i, attr := range node.Attr {
			if attr.Key == "href" {
				asset := localAsset(attr.Val, sourceUrl)
				if asset != nil {
					node.Attr[i] = html.Attribute{
						Namespace: "",
						Key:       "href",
						Val:       fetchAsset(asset, dir),
					}
				}
			}
		}
	}
	child := node.FirstChild
	for child != nil {
		fixInternalLinks(child, dir, sourceUrl)
		child = child.NextSibling
	}
}

func fixImages(node *html.Node, dir string, sourceUrl *url.URL) {
	if node.Type == html.ElementNode && node.Data == "img" {
		for i, attr := range node.Attr {
			if attr.Key == "src" {
				asset := localAsset(attr.Val, sourceUrl)
				if asset != nil {
					// Fetch image to same directory as post and fix src to point to it.
					node.Attr[i] = html.Attribute{
						Namespace: "",
						Key:       "src",
						Val:       fetchAsset(asset, dir),
					}
				}
			}
			if attr.Key == "srcset" {
				parts := strings.Split(attr.Val, ",")
				genParts := []string{}
				for _, part := range parts {
					fields := strings.Fields(part)
					if len(fields) != 2 {
						genParts = append(genParts, part)
					} else {
						asset := localAsset(fields[0], sourceUrl)
						if asset != nil {
							fields[0] = fetchAsset(asset, dir)
						}
						genParts = append(genParts, strings.Join(fields, " "))
					}
				}

				node.Attr[i] = html.Attribute{
					Namespace: "",
					Key:       "srcset",
					Val:       strings.Join(genParts, ", "),
				}
			}
		}
	}
	child := node.FirstChild
	for child != nil {
		fixImages(child, dir, sourceUrl)
		child = child.NextSibling
	}
}

func findBody(node *html.Node) *html.Node {
	if node.Type == html.ElementNode && node.Data == "body" {
		return node
	}
	child := node.FirstChild
	for child != nil {
		found := findBody(child)
		if found != nil {
			return found
		}
		child = child.NextSibling
	}
	return nil
}

type Tag struct {
	ID          int
	Name        string
	Slug        string
	Description string
	Taxonomy    string
}

func getTags() map[int]*Tag {
	result := []Tag{}
	fetch("tags", &result, "tags?context=view&_fields=id,name,slug,description,taxonomy")
	rm := map[int]*Tag{}
	for idx, r := range result {
		_, ok := rm[r.ID]
		if ok {
			fatal("duplicate tag: %d", r.ID)
		}
		rm[r.ID] = &result[idx]
	}
	return rm
}

type Category struct {
	ID   int
	Name string
	Slug string
}

func getCategories() map[int]*Category {
	result := []Category{}
	fetch("categories", &result, "categories?context=view&_fields=id,name,slug")
	rm := map[int]*Category{}
	for idx, r := range result {
		_, ok := rm[r.ID]
		if ok {
			fatal("duplicate category: %d", r.ID)
		}
		rm[r.ID] = &result[idx]
	}
	return rm
}

type User struct {
	ID   int
	Name string
	Slug string
}

func getUsers() map[int]*User {
	result := []User{}
	fetch("users", &result, "users?context=view&_fields=id,name,slug")
	rm := map[int]*User{}
	for idx, r := range result {
		_, ok := rm[r.ID]
		if ok {
			fatal("duplicate user: %d", r.ID)
		}
		rm[r.ID] = &result[idx]
	}
	return rm
}

type Rendered struct {
	Rendered string
}

type Post struct {
	ID         int
	DateGmt    string `json:"date_gmt" mapstructure:"date_gmt"`
	Slug       string
	Status     string
	Title      Rendered
	Content    Rendered
	Excerpt    Rendered
	Author     int
	Categories []int
	Tags       []int
	Link       string

	AuthorName    string
	CategoryNames []string
	TagNames      []string
}

// Fetch all the Wordpress posts
func getPosts() []Post {
	result := []Post{}
	fetch("posts", &result, "posts?context=view&_fields=id,date_gmt,slug,status,title,content,excerpt,author,categories,tags,link")

	rm := map[int]struct{}{}
	for _, r := range result {
		_, ok := rm[r.ID]
		if ok {
			fatal("duplicate post: %d", r.ID)
		}
		rm[r.ID] = struct{}{}
	}
	return result
}

// Fetch a result set from WordPress, unmarshall it to our result
func fetch(name string, result interface{}, parameters string) {
	u, err := url.Parse(apiUrl + "wp/v2/" + parameters)
	if err != nil {
		fatal("failed to build api url for %s: %v", name, err)
	}
	raw := getAll(u, name)
	err = mapstructure.Decode(raw, result)
	if err != nil {
		fatal("failed to parse result for %s: %v", name, err)
	}
}

// Handle pagination for an arbitrary WordPress REST query
func getAll(u *url.URL, name string) []interface{} {
	limit := 1000000000
	if sample > 0 && name == "posts" {
		limit = sample
	}
	client := http.Client{
		Timeout: 30 * time.Second,
	}
	ret := []interface{}{}
	page := 1
	pageSize := 100
	if limit < pageSize {
		pageSize = limit
	}
	q := u.Query()
	q.Set("per_page", strconv.Itoa(pageSize))
	for {
		q.Set("page", strconv.Itoa(page))
		u.RawQuery = q.Encode()

		status("fetching %s %d ...", name, (page-1)*pageSize)
		res, err := client.Get(u.String())
		if err != nil {
			fatal("failed to fetch %s: %v", u, err)
		}

		decoder := json.NewDecoder(res.Body)
		thisPage := []interface{}{}
		err = decoder.Decode(&thisPage)
		if err != nil {
			fatal("failed to parse response from %s: %v", u, err)
		}
		res.Body.Close()
		ret = append(ret, thisPage...)
		if len(thisPage) < pageSize || len(ret) >= limit {
			endStatus("fetched %d %s", len(ret), name)
			return ret
		}
		page++
	}
}

var lfNeeded = false
var statusLen = 0

func status(format string, a ...interface{}) {
	if !quiet {
		lfNeeded = true
		msg := fmt.Sprintf(format, a...)
		_, _ = io.WriteString(os.Stderr, "\r"+msg)
		if len(msg) < statusLen {
			_, _ = io.WriteString(os.Stderr, strings.Repeat(" ", statusLen-len(msg))+"\r")
		}
		statusLen = len(msg)
	}
}

func endStatus(format string, a ...interface{}) {
	status("")
	lfNeeded = false
	info(format, a...)
}

func skipStatus() {
	if lfNeeded {
		lfNeeded = false
		_, _ = io.WriteString(os.Stdout, "\n")
	}
}

func info(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...) + "\n"
	if logWriter != nil {
		_, _ = io.WriteString(logWriter, msg)
	}
	if !quiet {
		skipStatus()
		_, _ = io.WriteString(os.Stdout, msg)
	}
}

func warn(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...) + "\n"
	if logWriter != nil {
		_, _ = io.WriteString(logWriter, "WARN: " + msg)
	}
	if !silent {
		skipStatus()
		color.Yellow.Print("WARN: ")
		_, _ = io.WriteString(os.Stdout, msg)
	}
}

func fatal(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...) + "\n"
	if logWriter != nil {
		_, _ = io.WriteString(logWriter, "FATAL: " + msg)
	}
	skipStatus()
	color.Red.Print("ERROR: ")
	_, _ = io.WriteString(os.Stdout, msg)
	os.Exit(1)
}

func parseURL(rawurl string) (*url.URL, error) {
	// Like url.Parse() but a bit more forgiving
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}
	u, err = url.Parse("http://" + rawurl)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}
	return nil, fmt.Errorf("invalid URL: '%s'", rawurl)
}

// findApi does discovers the API fo a wordpress site, as documented at
// https://developer.wordpress.org/rest-api/using-the-rest-api/discovery/
func findApi(siteUrl *url.URL) string {
	client := http.Client{
		Timeout: 30 * time.Second,
	}
	head, err := client.Head(siteUrl.String())
	if err != nil {
		fatal("Couldn't fetch %s while looking for site API: %v", siteUrl, err)
	}
	if head.StatusCode != http.StatusOK {
		fatal("Got %s response while fetching %s", head.Status, siteUrl)
	}
	links, ok := head.Header["Link"]
	if !ok {
		fatal("No Link: headers in response from %s", siteUrl)
	}

	// I'm a perl developer at heart
	linkPattern := regexp.MustCompile(`\s*<([^>]+)>\s*;\s*rel="https://api\.w\.org/"`)
	for _, link := range links {
		matches := linkPattern.FindStringSubmatch(link)
		if matches != nil {
			return matches[1]
		}
	}
	fatal("Unable to discover API for %s - maybe use the --api flag?", siteUrl)
	panic("I'm unreachable")
}

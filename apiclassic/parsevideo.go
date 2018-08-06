package apiclassic

import (
	"github.com/PuerkitoBio/goquery"
	"errors"
	"strconv"
	"time"
	"github.com/terorie/yt-mango/data"
	"regexp"
	"github.com/valyala/fastjson"
	"strings"
	"net/http"
	"github.com/terorie/yt-mango/api"
)

const likeBtnSelector = ".like-button-renderer-like-button-unclicked"
const dislikeBtnSelector = ".like-button-renderer-dislike-button-unclicked"
const userInfoSelector = "div .yt-user-info"
const channelNameSelector = ".yt-uix-sessionlink"
const recommendSelector = ".related-list-item"

var playerConfigErr = errors.New("failed to parse player config")

func ParseVideo(v *data.Video, res *http.Response) (_ []string, err error) {
	if res.StatusCode != 200 { return nil, errors.New("HTTP failure") }

	defer res.Body.Close()
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil { return }

	p := parseVideoInfo{v, doc}
	return p.parse()
}

type parseVideoInfo struct {
	v *data.Video
	doc *goquery.Document
}

func (p *parseVideoInfo) parse() ([]string, error) {
	available, err := p.checkAvailability()
	if err != nil { return nil, err }
	if !available { return nil, api.VideoUnavailable }

	if err := p.parseLikeDislike();
		err != nil { return nil, err }
	if err := p.parseUploader();
		err != nil { return nil, err }
	if err := p.parseDescription();
		err != nil { return nil, err }
	if err := p.parsePlayerConfig();
		err != nil { return nil, err }
	if err := p.parseMetas();
		err != nil { return nil, err }
	var recommends []string
	if err := p.parseRecommends(&recommends);
		err != nil { return nil, err }
	p.parseLicense()
	return recommends, nil
}

// Check if video unavailable
// Get the player-unavailable warning and check if it's hidden
func (p *parseVideoInfo) checkAvailability() (bool, error) {
	playerUnav := p.doc.Find("#player-unavailable")
	if len(playerUnav.Nodes) != 1 {
		return false, errors.New("cannot check if player is available")
	}
	return playerUnav.HasClass("hid"), nil
}

func (p *parseVideoInfo) parseLikeDislike() error {
	likeText := p.doc.Find(likeBtnSelector).First().Text()
	dislikeText := p.doc.Find(dislikeBtnSelector).First().Text()

	if len(likeText) == 0 || len(dislikeText) == 0 {
		return errors.New("failed to parse like buttons")
	}

	var err error
	p.v.Likes, err = api.ExtractNumber(likeText)
	if err != nil { return err }
	p.v.Dislikes, err = api.ExtractNumber(dislikeText)
	if err != nil { return err }

	return nil
}

func (p *parseVideoInfo) parseUploader() error {
	userInfo := p.doc.Find(userInfoSelector)
	userLinkNode := userInfo.Find(".yt-uix-sessionlink")

	// get link
	userLink, _ := userLinkNode.Attr("href")
	if userLink == "" { return errors.New("couldn't find channel link") }
	p.v.UploaderURL = "https://www.youtube.com" + userLink

	// get name
	channelName := userInfo.Find(channelNameSelector).Text()
	if channelName == "" { return errors.New("could not find channel name") }
	p.v.Uploader = channelName
	return nil
}

func (p *parseVideoInfo) parseMetas() (err error) {
	enumMetas(p.doc.Selection, func(tag metaTag)bool {
		content := tag.content
		switch tag.typ {
		case metaProperty:
			switch tag.name {
			case "og:title":
				p.v.Title = content
			case "og:video:tag":
				p.v.Tags = append(p.v.Tags, content)
			case "og:url":
				p.v.URL = content
			case "og:image":
				p.v.Thumbnail = content
			}
		case metaName:
			switch tag.name {
			}
		case metaItemProp:
			switch tag.name {
			case "datePublished":
				if val, err := time.Parse("2006-01-02", content);
					err == nil { p.v.UploadDate = val }
			case "genre":
				p.v.Genre = content
			case "channelId":
				p.v.UploaderID = content
			case "duration":
				if val, err := api.ParseDuration(content); err == nil {
					p.v.Duration = val
				} else {
					return false
				}
			case "isFamilyFriendly":
				if val, err := strconv.ParseBool(content);
					err == nil { p.v.FamilyFriendly = val }
			case "interactionCount":
				if val, err := strconv.ParseUint(content, 10, 64);
					err == nil { p.v.Views = val }
			}
		}
		return true
	})

	return err
}

func (p *parseVideoInfo) parsePlayerConfig() error {
	var json string

	p.doc.Find("script").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		script := s.Text()
		startMatch := regexp.MustCompile("var ytplayer = ytplayer \\|\\| {};\\s*ytplayer\\.config = {")
		endMatch := regexp.MustCompile("};\\s*ytplayer.load = function\\(")

		startIndices := startMatch.FindStringIndex(script)
		if startIndices == nil { return true }
		endIndices := endMatch.FindStringIndex(script)
		if endIndices == nil { return true }

		// minus one to preserve braces
		startIndex, endIndex := startIndices[1] - 1, endIndices[0] + 1
		if startIndex > endIndex { return true }

		json = script[startIndex:endIndex]

		// Stop searching, json found
		return false
	})
	// No json found
	if json == "" { return playerConfigErr }

	// Try decoding json
	var parser fastjson.Parser
	config, err := parser.Parse(json)
	if err != nil { return err }

	// Extract data
	args := config.Get("args")
	if args == nil { return playerConfigErr }

	// Get fmt_list string
	fmtList := args.GetStringBytes("fmt_list")
	if fmtList == nil { return playerConfigErr }

	// Split and decode it
	fmts := strings.Split(string(fmtList), ",")
	for _, fmt := range fmts {
		parts := strings.Split(fmt, "/")
		if len(parts) != 2 { return playerConfigErr }
		formatID := parts[0]
		// Look up the format ID
		format := data.FormatsById[formatID]
		if format == nil { return playerConfigErr }
		p.v.Formats = append(p.v.Formats, *format)
	}

	return nil
}

func (p *parseVideoInfo) parseRecommends(r *[]string) error {
	s := p.doc.Find(recommendSelector).Find(".content-wrapper").Find("a")
	s.Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists { return }
		if !strings.HasPrefix(href, "/watch?v=") { return }
		id := href[len("/watch?v="):]
		*r = append(*r, id)
	})
	return nil
}

func (p *parseVideoInfo) parseLicense() {
	p.doc.Find(".watch-meta-item").EachWithBreak(func(i int, s *goquery.Selection) bool {
		title := strings.Trim(s.Find("h4").Text(), "\n ")
		if title == "License" {
			p.v.License = s.Find("a").Text()
			return false
		}
		return true
	})
}

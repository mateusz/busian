package main

import (
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"image"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	_ "image/png"

	"github.com/faiface/pixel"
	"github.com/faiface/pixel/imdraw"
	"github.com/faiface/pixel/pixelgl"
	"github.com/lafriks/go-tiled"
	"golang.org/x/image/colornames"
)

const (
	// How many friction map cells per map tile.
	frictionMapRes = 4
)

var (
	terra       map[uint32]*pixel.Sprite
	car         *pixel.Sprite
	tmx         *tiled.Map
	frictionMap [][]int
)

func main() {
	terra = make(map[uint32]*pixel.Sprite)

	var err error
	tmx, err = tiled.LoadFromFile("assets/map.tmx")
	if err != nil {
		fmt.Printf("Error parsing map: %s\n", err)
		os.Exit(2)
	}
	err = loadMissing(tmx)
	if err != nil {
		fmt.Printf("Error loading aux tilesets: %s\n", err)
		os.Exit(2)
	}

	err = loadFrictionMap(tmx, &frictionMap)
	if err != nil {
		fmt.Printf("Error loading friction map: %s\n", err)
		os.Exit(2)
	}

	file, err := os.Open(tmx.GetFileFullPath("car_test.png"))
	if err != nil {
		fmt.Printf("Error opening car: %s\n", err)
		os.Exit(2)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		fmt.Printf("Error decoding car: %s\n", err)
		os.Exit(2)
	}
	pic := pixel.PictureDataFromImage(img)
	car = pixel.NewSprite(pic, pic.Bounds())

	pixelgl.Run(run)
}

func run() {
	monitor := pixelgl.PrimaryMonitor()

	monW, monH := monitor.Size()
	pixH := monH / float64(tmx.Height*tmx.TileHeight)
	pixW := monW / float64(tmx.Width*tmx.TileWidth)
	pixSize := math.Floor(math.Min(pixH, pixW))

	cfg := pixelgl.WindowConfig{
		Title:   "Busian",
		Bounds:  pixel.R(0, 0, monW, monH),
		VSync:   true,
		Monitor: monitor,
	}

	win, err := pixelgl.NewWindow(cfg)
	if err != nil {
		panic(err)
	}

	win.SetSmooth(false)
	cam1 := pixel.IM.Scaled(pixel.ZV, pixSize)
	cam1 = cam1.Moved(pixel.V(float64(tmx.TileWidth)*pixSize/2, float64(tmx.TileHeight)*pixSize/2))
	win.SetMatrix(cam1)

	imd := imdraw.New(nil)
	// I really don't know why.
	cam2 := pixel.IM.Moved(pixel.V(float64(frictionMapRes)*-2, float64(frictionMapRes)*-2))
	imd.SetMatrix(cam2)

	px := 100.0
	py := 100.0
	last := time.Now()
	for !win.Closed() {
		dt := time.Since(last).Seconds()
		last = time.Now()

		if win.Pressed(pixelgl.KeyEscape) {
			break
		}
		// Offset car size
		fr := posToFriction(px+4, py)
		mv := (dt * 100) / float64(fr)
		if win.Pressed(pixelgl.KeyRight) {
			px += mv
		}
		if win.Pressed(pixelgl.KeyLeft) {
			px -= mv
		}
		if win.Pressed(pixelgl.KeyUp) {
			py += mv
		}
		if win.Pressed(pixelgl.KeyDown) {
			py -= mv
		}

		imd.Clear()
		win.Clear(colornames.Skyblue)

		drawMap(win)
		drawCar(win, px, py)
		if win.Pressed(pixelgl.KeyF) {
			drawFrictionMap(imd)
		}

		imd.Draw(win)
		win.Update()
	}
}

func loadMissing(m *tiled.Map) error {
	for _, ts := range m.Tilesets {
		err := sideloadTSXForTileset(m, ts)
		if err != nil {
			return err
		}
	}

	return nil
}

func sideloadTSXForTileset(m *tiled.Map, ts *tiled.Tileset) error {
	if ts.Source == "" {
		return nil
	}
	f, err := os.Open(m.GetFileFullPath(ts.Source))

	if err != nil {
		return err
	}
	defer f.Close()

	d := xml.NewDecoder(f)

	if err := d.Decode(ts); err != nil {
		return err
	}

	for _, t := range ts.Tiles {
		if t.Image.Source == "" {
			continue
		}

		file, err := os.Open(m.GetFileFullPath(t.Image.Source))
		if err != nil {
			return err
		}
		defer file.Close()

		img, _, err := image.Decode(file)
		if err != nil {
			return err
		}

		pic := pixel.PictureDataFromImage(img)
		terra[t.ID] = pixel.NewSprite(pic, pic.Bounds())
	}

	return nil
}

func loadFrictionMap(m *tiled.Map, frictionMap *[][]int) error {
	*frictionMap = make([][]int, m.Width*frictionMapRes)
	for i := range *frictionMap {
		(*frictionMap)[i] = make([]int, m.Height*frictionMapRes)
	}

	l := m.Layers[0]
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			layerTile := l.Tiles[y*tmx.Width+x]
			tile, err := findTileInTileset(layerTile)
			if err != nil {
				return err
			}

			friction := tile.Properties.GetString("friction")
			if err != nil {
				return err
			}

			r := csv.NewReader(strings.NewReader(friction))
			record, err := r.Read()

			for fy := 0; fy < frictionMapRes; fy++ {
				for fx := 0; fx < frictionMapRes; fx++ {
					i := (frictionMapRes-1-fy)*frictionMapRes + fx
					fv, err := strconv.Atoi(record[i])
					if err != nil {
						return err
					}

					(*frictionMap)[x*frictionMapRes+fx][y*frictionMapRes+fy] = fv
				}
			}
		}
	}

	return nil
}

func findTileInTileset(lt *tiled.LayerTile) (*tiled.TilesetTile, error) {
	for _, t := range lt.Tileset.Tiles {
		if t.ID == lt.ID {
			return t, nil
		}
	}

	return nil, fmt.Errorf("Something is very wrong, tile ID '%d' not found in the tileset", lt.ID)
}

func tileVec(x int, y int) pixel.Vec {
	return pixel.V(float64(x*(tmx.TileWidth)), float64(y*tmx.TileHeight))
}

func posToFriction(px, py float64) int {
	x := int(math.Round(px))
	y := int(math.Round(py))
	if x < 0 || x > tmx.Width*tmx.TileWidth {
		return -1
	}
	if y < 0 || y > tmx.Height*tmx.TileHeight {
		return -1
	}
	fx := int(math.Round(float64(x) / float64(frictionMapRes)))
	fy := int(math.Round(float64(y) / float64(frictionMapRes)))
	return frictionMap[fx][fy]
}

func drawMap(win *pixelgl.Window) {
	l := tmx.Layers[0]
	for y := 0; y < tmx.Height; y++ {
		for x := 0; x < tmx.Width; x++ {
			lt := l.Tiles[y*tmx.Width+x]
			terra[lt.ID].Draw(win, pixel.IM.Moved(tileVec(x, tmx.Height-y-1)))
		}
	}
}

func drawCar(win *pixelgl.Window, px, py float64) {
	car.Draw(win, pixel.IM.Moved(pixel.V(px, py)))
}

func drawFrictionMap(imd *imdraw.IMDraw) {
	for y := 0; y < tmx.Height*frictionMapRes; y++ {
		for x := 0; x < tmx.Width*frictionMapRes; x++ {

			if frictionMap[x][y] == 1 {
				imd.Color = pixel.RGBA{B: 255, A: 0.2}
			} else {
				imd.Color = pixel.RGBA{R: 255, A: 0.2}
			}
			imd.Push(pixel.V(float64(x)*frictionMapRes, float64(y)*frictionMapRes))
			imd.Push(pixel.V(float64(x+1)*frictionMapRes, float64(y+1)*frictionMapRes))
			imd.Rectangle(0)
		}
	}
}

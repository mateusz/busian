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
	terra       tileset
	car         mobile
	police      mobile
	tmx         *tiled.Map
	frictionMap [][]int
)

type mobile struct {
	// World position
	wp pixel.Vec
	// Velocity
	v      pixel.Vec
	sprite *pixel.Sprite
}

type tileset struct {
	tiles map[uint32]*pixel.Sprite
}

func newTileset() tileset {
	t := tileset{}
	t.tiles = make(map[uint32]*pixel.Sprite)
	return t
}

func main() {
	var err error
	tmx, err = tiled.LoadFromFile("assets/map.tmx")
	if err != nil {
		fmt.Printf("Error parsing map: %s\n", err)
		os.Exit(2)
	}
	terra, err = tilesetFromTmx(tmx)
	if err != nil {
		fmt.Printf("Error loading aux tilesets: %s\n", err)
		os.Exit(2)
	}

	err = loadFrictionMap(tmx, &frictionMap)
	if err != nil {
		fmt.Printf("Error loading friction map: %s\n", err)
		os.Exit(2)
	}

	car.sprite, err = load("car_test.png")
	if err != nil {
		fmt.Printf("Error loading car: %s\n", err)
		os.Exit(2)
	}

	police.sprite, err = load("car_police.png")
	if err != nil {
		fmt.Printf("Error loading police: %s\n", err)
		os.Exit(2)
	}

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
	win.SetMatrix(cam1)

	imd := imdraw.New(nil)

	car.wp = pixel.Vec{10.0, 10.0}
	police.wp = pixel.Vec{100.0, 100.0}
	last := time.Now()
	for !win.Closed() {
		dt := time.Since(last).Seconds()
		last = time.Now()

		if win.Pressed(pixelgl.KeyEscape) {
			break
		}

		// Steer police
		police.v = pixel.ZV
		fr := posToFriction(police.wp.X, police.wp.Y-1)
		if fr == 5 {
			fr = 3
		}
		mv := (dt * 25) / float64(fr)
		if win.Pressed(pixelgl.KeyRight) {
			police.v.X = mv
		}
		if win.Pressed(pixelgl.KeyLeft) {
			police.v.X = -mv
		}
		if win.Pressed(pixelgl.KeyUp) {
			police.v.Y = mv
		}
		if win.Pressed(pixelgl.KeyDown) {
			police.v.Y = -mv
		}

		// Steer car
		car.v = pixel.ZV
		fr = posToFriction(car.wp.X, car.wp.Y-1)
		if fr == 5 {
			fr = 3
		}
		mv = (dt * 15) / float64(fr)
		if win.Pressed(pixelgl.KeyD) {
			car.v.X = mv
		}
		if win.Pressed(pixelgl.KeyA) {
			car.v.X = -mv
		}
		if win.Pressed(pixelgl.KeyW) {
			car.v.Y = mv
		}
		if win.Pressed(pixelgl.KeyS) {
			car.v.Y = -mv
		}

		// Apply velocity
		car.wp = car.wp.Add(car.v)
		police.wp = police.wp.Add(police.v)

		// Draw
		imd.Clear()
		win.Clear(colornames.Skyblue)

		drawMap(win)
		drawCar(win, &police)
		drawCar(win, &car)
		if win.Pressed(pixelgl.KeyF) {
			drawFrictionMap(imd)
			drawCarPos(imd, &car)
			drawCarPos(imd, &police)
		}

		imd.Draw(win)
		win.Update()
	}
}

// TMX library does not load images. Help it out.
func tilesetFromTmx(m *tiled.Map) (tileset, error) {
	tiles := newTileset()
	for _, ts := range m.Tilesets {
		err := tiles.sideloadTSXForTileset(m, ts)
		if err != nil {
			return tiles, err
		}
	}

	return tiles, nil
}

// Load tileset graphic tiles into the "terra" array.
func (tileset tileset) sideloadTSXForTileset(m *tiled.Map, ts *tiled.Tileset) error {
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
		tileset.tiles[t.ID] = pixel.NewSprite(pic, pic.Bounds())
	}

	return nil
}

// Friction map is an overlay to work out if the car is on the road or not.
// Friction data is stored in the Tiled's tile metadata node "friction" at a lower resolution (4x4).
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

					(*frictionMap)[x*frictionMapRes+fx][(m.Height-1-y)*frictionMapRes+fy] = fv
				}
			}
		}
	}

	return nil
}

// Get sprite from file.
func load(path string) (*pixel.Sprite, error) {
	file, err := os.Open(tmx.GetFileFullPath(path))
	if err != nil {
		return nil, fmt.Errorf("error opening car: %s", err)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("error decoding car: %s", err)
	}
	pic := pixel.PictureDataFromImage(img)
	return pixel.NewSprite(pic, pic.Bounds()), nil
}

func findTileInTileset(lt *tiled.LayerTile) (*tiled.TilesetTile, error) {
	for _, t := range lt.Tileset.Tiles {
		if t.ID == lt.ID {
			return t, nil
		}
	}

	return nil, fmt.Errorf("Something is very wrong, tile ID '%d' not found in the tileset", lt.ID)
}

// Convert tile coords (x,y) to world coordinates.
func tileVec(x int, y int) pixel.Vec {
	// Some offesting due to the tiles being referenced via the centre
	ox := tmx.TileWidth / 2
	oy := tmx.TileHeight / 2
	return pixel.V(float64(x*(tmx.TileWidth)+ox), float64(y*tmx.TileHeight+oy))
}

// Read friction from the preloaded friction map based on world coordinates (px,py).
func posToFriction(px, py float64) int {
	x := int(math.Round(px))
	y := int(math.Round(py))
	fx := int(math.Floor(float64(x) / float64(frictionMapRes)))
	fy := int(math.Floor(float64(y) / float64(frictionMapRes)))
	return frictionMap[fx][fy]
}

func drawMap(win *pixelgl.Window) {
	l := tmx.Layers[0]
	for y := 0; y < tmx.Height; y++ {
		for x := 0; x < tmx.Width; x++ {
			lt := l.Tiles[y*tmx.Width+x]
			terra.tiles[lt.ID].Draw(win, pixel.IM.Moved(tileVec(x, tmx.Height-y-1)))
		}
	}
}

func drawCar(win *pixelgl.Window, m *mobile) {
	m.sprite.Draw(win, pixel.IM.Moved(m.wp))
}

// Debug helper
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

// Debug helper
func drawCarPos(imd *imdraw.IMDraw, m *mobile) {
	imd.Color = colornames.White
	imd.Push(pixel.V(m.wp.X-1, m.wp.Y-1-1))
	imd.Push(pixel.V(m.wp.X+1, m.wp.Y+1-1))
	imd.Rectangle(0)
}

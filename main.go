package main

import (
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
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
	sprDirRight    = 0
	sprDirUp       = 1
	sprDirDown     = 2
	sprDirLeft     = 3
)

var (
	terra       spriteset
	mobs        []*mobile
	car         mobile
	police      mobile
	tmx         *tiled.Map
	frictionMap [][]int
)

type mobile struct {
	// World position
	wp pixel.Vec
	// Velocity
	v         pixel.Vec
	spriteset *spriteset
	startID   uint32
	stickyDir uint32
}

func (m *mobile) dirToSpr(dx, dy float64) *pixel.Sprite {
	if dx > 0 {
		m.stickyDir = sprDirRight
	}
	if dx < 0 {
		m.stickyDir = sprDirLeft
	}
	if dy > 0 {
		m.stickyDir = sprDirUp
	}
	if dy < 0 {
		m.stickyDir = sprDirDown
	}
	// ... and if 0,0, then use the old stickyDir so that the car doesn't randomly
	// flip after stopping!

	return m.spriteset.sprites[m.startID+m.stickyDir]
}

type spriteset struct {
	sprites  map[uint32]*pixel.Sprite
	tileset  *tiled.Tileset
	basePath string
}

func newSpriteset() spriteset {
	t := spriteset{}
	t.sprites = make(map[uint32]*pixel.Sprite)
	return t
}

func main() {
	var err error
	tmx, err = tiled.LoadFromFile("assets/map4.tmx")
	if err != nil {
		fmt.Printf("Error parsing map: %s\n", err)
		os.Exit(2)
	}
	terra, err = fillMissingMapPieces(tmx)
	if err != nil {
		fmt.Printf("Error loading aux tilesets: %s\n", err)
		os.Exit(2)
	}

	err = loadFrictionMap(tmx, &frictionMap)
	if err != nil {
		fmt.Printf("Error loading friction map: %s\n", err)
		os.Exit(2)
	}

	mobSprites, err := newSpritesetFromTsx("assets", "busian_mobs.tsx")
	if err != nil {
		fmt.Printf("Error loading mobs: %s\n", err)
		os.Exit(2)
	}

	car.spriteset = &mobSprites
	car.startID = 12

	police.spriteset = &mobSprites
	police.startID = 8

	mobs = []*mobile{&car, &police}

	pixelgl.Run(run)
}

func run() {
	monitor := pixelgl.PrimaryMonitor()

	monW, monH := monitor.Size()
	pixH := monH / float64(tmx.Height*tmx.TileHeight)
	pixW := monW / float64(tmx.Width*tmx.TileWidth)
	pixSize := math.Floor(math.Min(pixH, pixW)) * 4

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

	car.wp = pixel.Vec{100.0, 10.0}
	police.wp = pixel.Vec{100.0, 30.0}
	last := time.Now()
	for !win.Closed() {
		dt := time.Since(last).Seconds()
		last = time.Now()

		if win.Pressed(pixelgl.KeyEscape) {
			break
		}

		// Move camera
		cam := pixel.IM.Scaled(pixel.ZV, pixSize)
		cam = cam.Moved(pixel.Vec{-police.wp.X, -police.wp.Y}.Scaled(pixSize).Add(pixel.Vec{monW / 2.0, monH / 2.0}))
		win.SetMatrix(cam)

		var mv float64
		// Steer police
		police.v = pixel.ZV
		fr := posToFriction(police.wp.X, police.wp.Y-1)
		if fr == -1 {
			fr = 10
		}
		mv = (dt * 25) / fr
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
		if fr == -1 {
			fr = 10
		}
		mv = (dt * 25) / fr
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
		win.Clear(colornames.Green)

		drawMap(win)
		if win.Pressed(pixelgl.KeyF) {
			drawFrictionMap(imd)
		}

		sort.Slice(mobs, func(i, j int) bool {
			return mobs[i].wp.Y > mobs[j].wp.Y
		})
		for _, mob := range mobs {
			drawMob(win, mob)
		}

		imd.Draw(win)
		win.Update()
	}
}

// TMX library does not load images. Help it out.
func fillMissingMapPieces(m *tiled.Map) (spriteset, error) {
	spr := spriteset{}
	var err error
	for _, ts := range m.Tilesets {
		if ts.Source == "" {
			return spr, errors.New("Tileset has no source")
		}
		spr, err = newSpritesetFromTileset(m.GetFileFullPath(""), ts)
		if err != nil {
			return spr, err
		}

		// Only one permitted at the moment.
		break
	}

	return spr, nil
}

func newSpritesetFromTsx(basePath, path string) (spriteset, error) {
	spr := spriteset{}
	ts := &tiled.Tileset{Source: path}

	f, err := os.Open(filepath.Join(basePath, ts.Source))
	if err != nil {
		return spr, err
	}
	defer f.Close()

	d := xml.NewDecoder(f)
	if err := d.Decode(ts); err != nil {
		return spr, err
	}

	spr, err = newSpritesetFromTileset(basePath, ts)
	if err != nil {
		return spr, err
	}

	spr.tileset = ts
	return spr, nil
}

// Load actual sprite files and associate with tileset.
func newSpritesetFromTileset(basePath string, ts *tiled.Tileset) (spriteset, error) {
	spr := newSpriteset()
	spr.tileset = ts
	spr.basePath = basePath

	f, err := os.Open(filepath.Join(basePath, ts.Source))

	if err != nil {
		return spr, err
	}
	defer f.Close()

	d := xml.NewDecoder(f)

	if err := d.Decode(ts); err != nil {
		return spr, err
	}

	for _, t := range ts.Tiles {
		if t.Image.Source == "" {
			continue
		}

		file, err := os.Open(filepath.Join(basePath, t.Image.Source))
		if err != nil {
			return spr, err
		}
		defer file.Close()

		img, _, err := image.Decode(file)
		if err != nil {
			return spr, err
		}

		pic := pixel.PictureDataFromImage(img)
		spr.sprites[t.ID] = pixel.NewSprite(pic, pic.Bounds())
	}

	return spr, nil
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
func posToFriction(px, py float64) float64 {
	x := int(math.Round(px))
	y := int(math.Round(py))
	fx := int(math.Floor(float64(x) / float64(frictionMapRes)))
	fy := int(math.Floor(float64(y) / float64(frictionMapRes)))
	if fx < 0 || fx >= len(frictionMap) {
		return -1
	}
	if fy < 0 || fy >= len(frictionMap[fx]) {
		return -1
	}

	return float64(frictionMap[fx][fy])
}

func drawMap(win *pixelgl.Window) {
	l := tmx.Layers[0]
	for y := 0; y < tmx.Height; y++ {
		for x := 0; x < tmx.Width; x++ {
			lt := l.Tiles[y*tmx.Width+x]
			// Note: scaling 1.001 is used her to prevent transparent artifacts between tiles at times.
			terra.sprites[lt.ID].Draw(win, pixel.IM.Scaled(pixel.ZV, 1.001).Moved(tileVec(x, tmx.Height-y-1)))
		}
	}
}

func drawMob(win *pixelgl.Window, m *mobile) {
	m.dirToSpr(m.v.X, m.v.Y).Draw(win, pixel.IM.Moved(m.wp))
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

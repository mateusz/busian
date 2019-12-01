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
	"github.com/faiface/pixel/text"
	"github.com/faiface/pixel/imdraw"
	"github.com/faiface/pixel/pixelgl"
	"github.com/lafriks/go-tiled"
	"golang.org/x/image/colornames"
	"golang.org/x/image/font/basicfont"
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
	mobs        []mobile
	steerables []steerable
	p1         player
	p2      player
	tmx         *tiled.Map
	frictionMap [][]int
)

type mobile interface {
	Draw(pixel.Target)
	GetZ() float64
}

type steerable interface {
	Steer(float64, *pixelgl.Window)
}

type vehicle struct {
	// World position
	wp pixel.Vec
	// Velocity
	v         pixel.Vec
	spriteset *spriteset
	startID   uint32
	stickyDir uint32
}

func (v *vehicle) Draw(t pixel.Target) {
	v.dirToSpr(v.v.X, v.v.Y).Draw(t, pixel.IM.Moved(v.wp))
}

func (v *vehicle) GetZ() float64 {
	return v.wp.Y
}

type player struct {
	vehicle
	c controls
}

func (p *player) Steer(dt float64, w *pixelgl.Window) {
	isBraking := false
	if w.Pressed(p.c.Right) && p.v.X < 0.0 {
		isBraking = true
	}
	if w.Pressed(p.c.Left) && p.v.X > 0.0 {
		isBraking = true
	}
	if w.Pressed(p.c.Up) && p.v.Y < 0.0 {
		isBraking = true
	}
	if w.Pressed(p.c.Down) && p.v.Y > 0.0 {
		isBraking = true
	}
	
	// Read one of the 8 cardinal directions for acceleration.
	d := pixel.Vec{X:0, Y:0}
	isAccelerating := false
	if w.Pressed(p.c.Right) {
		d = d.Add(pixel.Vec{X: 1.0})
		isAccelerating = true
	}
	if w.Pressed(p.c.Left) {
		d = d.Add(pixel.Vec{X: -1.0})
		isAccelerating = true
	}
	if w.Pressed(p.c.Up) {
		d = d.Add(pixel.Vec{Y: 1.0})
		isAccelerating = true
	}
	if w.Pressed(p.c.Down) {
		d = d.Add(pixel.Vec{Y: -1.0})
		isAccelerating = true
	}

	// Normalise direction to l=1.0
	// Prevent direction change if braking
	if isAccelerating && !isBraking {
		d = d.Scaled(1.0/d.Len())
	} else if p.v.Len()>0.0 {
		d = p.v.Scaled(1.0/p.v.Len())
	} // Otherwise no direction from velocity.

	// Get current velocity
	v := p.v.Len()

	// Figure out velocity changes
	if isBraking {
		v -= dt * 60.0
	} else if isAccelerating {
		v += dt * 30.0
	}

	topSpeed := 60.0
	frCoef := posToFriction(p.wp.X, p.wp.Y-1)
	if frCoef == -1 {
		frCoef = 10
	}
	maxSpeed := topSpeed / frCoef
	fr := v - maxSpeed
	if fr>0.0 {
		v -= fr * dt * 10.0
	}
	v -= 10.0 * dt

	if v<0.0 {
		v = 0.0
	}
	if v>topSpeed {
		v = topSpeed
	}

	p.v = d.Scaled(v)

	// Apply velocity
	p.wp = p.wp.Add(p.v.Scaled(dt))
}

type controls struct {
	Up pixelgl.Button 
	Down pixelgl.Button
	Left pixelgl.Button
	Right pixelgl.Button
}

func (v *vehicle) dirToSpr(dx, dy float64) *pixel.Sprite {
	if dx > 0 {
		v.stickyDir = sprDirRight
	}
	if dx < 0 {
		v.stickyDir = sprDirLeft
	}
	if dy > 0 {
		v.stickyDir = sprDirUp
	}
	if dy < 0 {
		v.stickyDir = sprDirDown
	}
	// ... and if 0,0, then use the old stickyDir so that the car doesn't randomly
	// flip after stopping!

	return v.spriteset.sprites[v.startID+v.stickyDir]
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

	p1.spriteset = &mobSprites
	p1.startID = 12
	p1.c = controls{Up:pixelgl.KeyW, Down:pixelgl.KeyS, Left:pixelgl.KeyA, Right:pixelgl.KeyD}
	p1.wp = pixel.Vec{X: 100.0, Y: 10.0}

	p2.spriteset = &mobSprites
	p2.startID = 8
	p2.c = controls{Up:pixelgl.KeyUp, Down:pixelgl.KeyDown, Left:pixelgl.KeyLeft, Right:pixelgl.KeyRight}
	p2.wp = pixel.Vec{X: 100.0, Y: 30.0}

	steerables = []steerable{&p1, &p2}
	mobs = []mobile{&p1, &p2}

	pixelgl.Run(run)
}

func run() {
	monitor := pixelgl.PrimaryMonitor()

	monW, monH := monitor.Size()
	pixSize := 4.0

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

	// Zoom in to get nice pixels
	win.SetSmooth(false)
	win.SetMatrix(pixel.IM.Scaled(pixel.ZV, pixSize))

	frictionMap := imdraw.New(nil)
	drawFrictionMap(frictionMap)

	worldMap := pixelgl.NewCanvas(pixel.R(0, 0, float64(tmx.Width * tmx.TileWidth), float64(tmx.Height * tmx.TileHeight)))
	drawMap(worldMap)

	p1view := pixelgl.NewCanvas(pixel.R(0,0,monW/2/pixSize, monH/pixSize))
	p2view := pixelgl.NewCanvas(pixel.R(0,0,monW/2/pixSize, monH/pixSize))

	hud := pixelgl.NewCanvas(pixel.R(0, 0, monW/pixSize, monH/pixSize))

	staticHud := imdraw.New(nil)
	staticHud.Color = colornames.Black
	staticHud.Push(pixel.V(monW/2/pixSize, 0.0))
	staticHud.Push(pixel.V(monW/2/pixSize, monH/pixSize))
	staticHud.Line(1)
	fps := text.New(pixel.ZV, text.NewAtlas(basicfont.Face7x13, text.ASCII))

	last := time.Now()
	fpsAvg := 60.0
	for !win.Closed() {
		if win.Pressed(pixelgl.KeyEscape) {
			break
		}

		dt := time.Since(last).Seconds()
		last = time.Now()

		fpsAvg -= fpsAvg/50.0
		fpsAvg += 1.0/dt/50.0

		p1.Steer(dt, win)
		p2.Steer(dt, win)

		// Center views on players
		cam1 := pixel.IM.Moved(pixel.Vec{
			X: -p1.wp.X + p1view.Bounds().W()/2,
			Y: -p1.wp.Y + p1view.Bounds().H()/2,
		})
		p1view.SetMatrix(cam1)

		cam2 := pixel.IM.Moved(pixel.Vec{
			X: -p2.wp.X + p2view.Bounds().W()/2,
			Y: -p2.wp.Y + p2view.Bounds().H()/2,
		})
		p2view.SetMatrix(cam2)

		// Draw
		win.Clear(colornames.Black)
		hud.Clear(pixel.RGBA{})
		p1view.Clear(colornames.Green)
		p2view.Clear(colornames.Green)

		worldMap.Draw(p1view, pixel.IM.Moved(pixel.Vec{
			X:worldMap.Bounds().W()/2.0,
			Y:worldMap.Bounds().H()/2.0,
		}))
		worldMap.Draw(p2view, pixel.IM.Moved(pixel.Vec{
			X:worldMap.Bounds().W()/2.0,
			Y:worldMap.Bounds().H()/2.0,
		}))

		if win.Pressed(pixelgl.KeyG) {
			frictionMap.Draw(p1view)
			frictionMap.Draw(p2view)
			fps.Clear()
			fmt.Fprintf(fps, "%.0f", fpsAvg)
			fps.Draw(hud, pixel.IM)
		}

		sort.Slice(mobs, func(i, j int) bool {
			return mobs[i].GetZ() > mobs[j].GetZ()
		})
		for _, mob := range mobs {
			mob.Draw(p1view)
			mob.Draw(p2view)
		}

		// Draw  views onto respective halves of the screen
		p1view.Draw(win, pixel.IM.Moved(pixel.Vec{
			X:p1view.Bounds().W()/2,
			Y:p1view.Bounds().H()/2,
		}))
		p2view.Draw(win, pixel.IM.Moved(pixel.Vec{
			X:monW/2/pixSize+p2view.Bounds().W()/2,
			Y:p2view.Bounds().H()/2,
		}))

		staticHud.Draw(hud)

		hud.Draw(win, pixel.IM.Moved(pixel.V(hud.Bounds().W()/2, hud.Bounds().H()/2)))

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

func drawMap(c *pixelgl.Canvas) {
	l := tmx.Layers[0]
	for y := 0; y < tmx.Height; y++ {
		for x := 0; x < tmx.Width; x++ {
			lt := l.Tiles[y*tmx.Width+x]
			terra.sprites[lt.ID].Draw(c, pixel.IM.Moved(tileVec(x, tmx.Height-y-1)))
		}
	}
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
package main

import (
	"encoding/csv"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// --- Helper Functions (Ymir & CSV) ---

func parseYmirOutput(output string) map[string]string {
	data := make(map[string]string)
	var infoLine string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "@#") {
			infoLine = line
			break
		}
	}
	if infoLine == "" {
		return data
	}
	cleaned := strings.TrimSpace(infoLine)
	cleaned = strings.TrimPrefix(cleaned, "@#")
	cleaned = strings.TrimSuffix(cleaned, "#@")
	cleaned = strings.TrimSpace(cleaned)
	pairs := strings.Split(cleaned, ";")
	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			data[parts[0]] = parts[1]
		}
	}
	return data
}

func downloadCSV(filepath string) error {
	url := "https://storage.googleapis.com/play_public/supported_devices.csv"
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func decodeUTF16(b []byte) (string, error) {
	if len(b)%2 != 0 {
		return "", fmt.Errorf("invalid UTF-16 length: must be even")
	}
	u16s := make([]uint16, len(b)/2)
	for i := 0; i < len(u16s); i++ {
		u16s[i] = uint16(b[2*i]) | (uint16(b[2*i+1]) << 8)
	}
	if len(u16s) > 0 && u16s[0] == 0xFEFF {
		u16s = u16s[1:]
	}
	return string(utf16.Decode(u16s)), nil
}

func getDeviceNameFromGooglePlay(modelCode string) string {
	csvFile := "supported_devices.csv"
	if _, err := os.Stat(csvFile); os.IsNotExist(err) {
		if err := downloadCSV(csvFile); err != nil {
			return ""
		}
	}
	f, err := os.Open(csvFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	var parsedContent string
	if len(content) > 2 && content[0] == 0xFF && content[1] == 0xFE {
		s, err := decodeUTF16(content)
		if err == nil {
			parsedContent = s
		}
	} else {
		parsedContent = string(content)
	}
	r := csv.NewReader(strings.NewReader(parsedContent))
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return ""
	}
	return findInRecords(records, modelCode)
}

func findInRecords(records [][]string, modelCode string) string {
	target := strings.ToLower(strings.TrimSpace(modelCode))
	for _, record := range records {
		if len(record) < 4 {
			continue
		}
		csvModel := strings.ToLower(strings.TrimSpace(record[3]))
		if csvModel == target {
			brand := record[0]
			marketingName := record[1]
			if marketingName == "" {
				return brand + " " + record[2]
			}
			return brand + " " + marketingName
		}
	}
	return ""
}

// --- Custom Theme ---

type myTheme struct{}

var _ fyne.Theme = (*myTheme)(nil)

func (m myTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	// Arka plan rengi: #0f0f10 -> RGB(15, 15, 16)
	if n == theme.ColorNameBackground {
		return color.RGBA{R: 15, G: 15, B: 16, A: 255}
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (m myTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (m myTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (m myTheme) Size(n fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(n)
}

// --- UI Row Helper ---

// createInfoRow, başlık, değer etiketi ve altındaki çizgiye sahip bir UI satırı oluşturur.
// Layout mantığı:
// VBox(Label, Line) -> Bu yapı içindekilerin genişliği en geniş elemana (Label) göre ayarlanır.
// HBox(VBox(...), Spacer) -> Bu yapı, içindeki VBox'ın sadece kendi genişliği kadar yer kaplamasını sağlar, sola yaslar.
func createInfoRow(title string, valLabel *widget.Label) *fyne.Container {
	titleObj := canvas.NewText(title, color.Gray{Y: 180})
	titleObj.TextSize = 12 

	// Ayırıcı Çizgi
	line := canvas.NewRectangle(color.RGBA{R: 0, G: 151, B: 178, A: 255})
	line.SetMinSize(fyne.NewSize(0, 2)) // Genişlik otomatik ayarlanacak

	// Değer ve çizgiyi alt alta koyuyoruz.
	// VBox içinde oldukları için çizgi, etiketin genişliğine kadar uzayacak (fill width).
	valueWithLine := container.NewVBox(valLabel, line)

	// valueWithLine'ı sola yaslamak ve uzamasını engellemek için HBox + Spacer kullanıyoruz.
	compactValue := container.NewHBox(valueWithLine, layout.NewSpacer())

	rowContent := container.NewVBox(
		titleObj,
		compactValue,
		layout.NewSpacer(), 
	)
	return rowContent
}

// --- Main Application ---

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(&myTheme{})

	myWindow := myApp.NewWindow("Galaxy Device Info")
	myWindow.Resize(fyne.NewSize(800, 500))

	// --- Disconnected View ---
	discLabel := widget.NewLabel("Please plug your Galaxy Device")
	discLabel.TextStyle = fyne.TextStyle{Bold: true}
	discLabel.Alignment = fyne.TextAlignCenter
	disconnectedContent := container.NewCenter(discLabel)

	// --- Connected View ---
	img := canvas.NewImageFromFile("download.png")
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(250, 400)) 

	// Bilgi Etiketleri
	modelLabel := widget.NewLabel("-")
	modelLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	productLabel := widget.NewLabel("-")
	productLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	vendorLabel := widget.NewLabel("-")
	vendorLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	fwVerLabel := widget.NewLabel("-")
	fwVerLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	capaLabel := widget.NewLabel("-")
	capaLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	didLabel := widget.NewLabel("-")
	didLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Sağ taraf (Bilgiler) düzeni
	detailsList := container.NewVBox(
		createInfoRow("Model Name", modelLabel),
		createInfoRow("Product Name", productLabel),
		createInfoRow("Vendor (Carrier)", vendorLabel),
		createInfoRow("Firmware Version", fwVerLabel),
		createInfoRow("Storage Capacity", capaLabel),
		createInfoRow("Device ID", didLabel),
	)

	connectedContent := container.NewBorder(
		nil, nil, // Top, Bottom
		container.NewPadded(img), // Left
		nil, // Right
		container.NewPadded(container.NewVBox(layout.NewSpacer(), detailsList, layout.NewSpacer())), // Center
	)

	connectedContent.Hide()
disconnectedContent.Show()

	rootContainer := container.NewMax(disconnectedContent, connectedContent)
	myWindow.SetContent(rootContainer)

	isDeviceConnected := false

	// --- Auto-Detection Loop ---
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		for range ticker.C {
			// Ymir komutu yerine yeni receive_data aracı
			cmd := exec.Command("./receive_data")
			output, err := cmd.CombinedOutput()
			outputStr := string(output)

			if err != nil {
				fmt.Println("Hata:", err)
				fmt.Println("Cikti:", outputStr)
			}

			ymirData := parseYmirOutput(outputStr)
			detectionSuccess := err == nil && len(ymirData) > 0

			if detectionSuccess {
				if !isDeviceConnected {
					isDeviceConnected = true
					
					commercialName := getDeviceNameFromGooglePlay(ymirData["MODEL"])
					displayModel := ymirData["MODEL"]
					if commercialName != "" {
						displayModel = commercialName + " (" + displayModel + ")"
					}

					capa := ymirData["CAPA"]
					if capa != "" {
						capa += " GB"
					} else {
						capa = "Unknown"
					}

					// Etiketleri güncelle (Thread-safe)
					modelLabel.SetText(displayModel)
					productLabel.SetText(ymirData["PRODUCT"])
					vendorLabel.SetText(ymirData["VENDOR"])
					fwVerLabel.SetText(ymirData["FWVER"])
					capaLabel.SetText(capa)
					didLabel.SetText(ymirData["DID"])

					disconnectedContent.Hide()
					connectedContent.Show()
				}
			} else {
				if isDeviceConnected {
					isDeviceConnected = false
					connectedContent.Hide()
					disconnectedContent.Show()
				}
			}
		}
	}()

	myWindow.ShowAndRun()
}

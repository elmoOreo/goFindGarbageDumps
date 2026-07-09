package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	vision "cloud.google.com/go/vision/apiv1"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type DumpSiteData struct {
	ChatID             int64     `bson:"chatId" json:"chatId"`
	MessageID          int       `bson:"messageId" json:"messageId"`
	Reporter           string    `bson:"reporter" json:"reporter"`
	DumpType           string    `bson:"dumpType" json:"dumpType"`
	ClassificationConf float64   `bson:"classificationConf" json:"classificationConf"`
	GarbageSubTypes    []string  `bson:"garbageSubTypes" json:"garbageSubTypes"`
	ObjectName         string    `bson:"objectName" json:"objectName"`
	Latitude           float64   `bson:"latitude,omitempty" json:"latitude"`
	Longitude          float64   `bson:"longitude,omitempty" json:"longitude"`
	HorizontalAccuracy float64   `bson:"horizontalAccuracy,omitempty" json:"horizontalAccuracy"`
	RecordedOn         time.Time `bson:"recordedOn" json:"recordedOn"`

	State         string `bson:"state" json:"state"`
	County        string `bson:"county" json:"county"`
	StateDistrict string `bson:"state_district" json:"state_district"`
	City          string `bson:"city" json:"city"`
	PostalCode    string `bson:"postalCode" json:"postalCode"`
	District      string `bson:"district" json:"district"`
	Neighbourhood string `bson:"neighbourhood" json:"neighbourhood"`
	Suburb        string `bson:"suburb" json:"suburb"`
	Street        string `bson:"street" json:"street"`
}

type BlockedUserData struct {
	ChatID    int64     `bson:"chatId"`
	Reporter  string    `bson:"reporter"`
	BlockedOn time.Time `bson:"blockedOn"`
	Reason    string    `bson:"reason"`
}

type GeoapifyResponse struct {
	Features []struct {
		Properties struct {
			State         string `json:"state"`
			County        string `json:"county"`
			StateDistrict string `json:"state_district"`
			City          string `json:"city"`
			Postcode      string `json:"postcode"`
			District      string `json:"district"`
			Neighbourhood string `json:"neighbourhood"`
			Suburb        string `json:"suburb"`
			Street        string `json:"street"`
		} `json:"properties"`
	} `json:"features"`
}

var (
	bot               *tgbotapi.BotAPI
	mongoClient       *mongo.Client
	databaseName      = "BinItBotDB"
	collectionName    = "dumpSites"
	blocklistCollName = "blockedUsers"
)

func reverseGeocode(lat, lon float64) (GeoapifyResponse, error) {
	var targetGeo GeoapifyResponse
	apiKey := os.Getenv("GEOAPIFY_KEY")
	if apiKey == "" {
		return targetGeo, fmt.Errorf("critical error: GEOAPIFY_KEY environment variable is not configured")
	}

	url := fmt.Sprintf("https://api.geoapify.com/v1/geocode/reverse?lat=%f&lon=%f&apiKey=%s", lat, lon, apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return targetGeo, err
	}

	res, err := client.Do(req)
	if err != nil {
		return targetGeo, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return targetGeo, fmt.Errorf("geoapify api returned status code: %d", res.StatusCode)
	}

	err = json.NewDecoder(res.Body).Decode(&targetGeo)
	return targetGeo, err
}

func generateV4ReadSignedURL(objectPath string) (string, error) {
	bucketName := os.Getenv("BUCKET_NAME")
	if bucketName == "" {
		return "", fmt.Errorf("BUCKET_NAME environment variable not set")
	}

	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to sign token via storage configuration: %w", err)
	}
	defer client.Close()

	opts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(15 * time.Minute),
	}

	return client.Bucket(bucketName).SignedURL(objectPath, opts)
}

func initMongoDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		log.Fatal("Error: MONGODB_URI environment variable is not set")
	}

	clientOptions := options.Client().ApplyURI(mongoURI)
	client, err := mongo.Connect(clientOptions)
	if err != nil {
		log.Fatalf("Failed to initialize MongoDB Atlas client: %v", err)
	}

	err = client.Ping(ctx, readpref.Primary())
	if err != nil {
		log.Fatalf("MongoDB Atlas connection ping failed: %v", err)
	}

	log.Println("Successfully authenticated and connected to MongoDB Atlas Cluster!")
	mongoClient = client

	collection := mongoClient.Database(databaseName).Collection(collectionName)
	err = collection.Indexes().DropOne(ctx, "recordedOn_1")
	if err != nil {
		log.Printf("Note: Index drop trace: %v", err)
	}

	indexModel := mongo.IndexModel{
		Keys: bson.D{{Key: "recordedOn", Value: 1}},
		Options: options.Index().
			SetExpireAfterSeconds(300).
			SetPartialFilterExpression(bson.M{"city": "Unverified"}),
	}

	_, err = collection.Indexes().CreateOne(ctx, indexModel)
	if err != nil {
		log.Fatalf("Critical: Failed to bind production Partial TTL index: %v", err)
	}

	log.Println("Successfully deployed conditional 5-minute sandbox TTL index layer!")
}

func initTelegram() {
	botToken := os.Getenv("TELEGRAM_APITOKEN")
	if botToken == "" {
		log.Fatal("Error: TELEGRAM_APITOKEN environment variable is not set")
	}

	var err error
	bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatalf("Failed to initialize BotAPI: %v", err)
	}

	log.Printf("Authorized on account name: %s", bot.Self.UserName)
}

func generateUniqueFileName() string {
	t := time.Now().UTC().Format("20060102_150405")
	return fmt.Sprintf("dumpsite_%s.jpg", t)
}

func processIncomingPhoto(ctx context.Context, fileID string) (string, error) {
	bucketName := os.Getenv("BUCKET_NAME")
	if bucketName == "" {
		return "", fmt.Errorf("BUCKET_NAME environment variable not set")
	}

	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		return "", fmt.Errorf("failed to get file URL from Telegram: %w", err)
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image from Telegram: %w", err)
	}
	defer resp.Body.Close()

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer storageClient.Close()

	fileName := generateUniqueFileName()
	objectPath := "staging/" + fileName
	wc := storageClient.Bucket(bucketName).Object(objectPath).NewWriter(ctx)

	if _, err := io.Copy(wc, resp.Body); err != nil {
		wc.Close()
		return "", fmt.Errorf("failed streaming bytes to GCS bucket: %w", err)
	}

	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("failed finalizing GCS file write: %w", err)
	}

	return objectPath, nil
}

func promoteGCSObject(ctx context.Context, stagingPath string) (string, error) {
	bucketName := os.Getenv("BUCKET_NAME")
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return "", err
	}
	defer storageClient.Close()

	productionPath := strings.Replace(stagingPath, "staging/", "production/", 1)
	bucket := storageClient.Bucket(bucketName)

	src := bucket.Object(stagingPath)
	dst := bucket.Object(productionPath)
	if _, err := dst.CopierFrom(src).Run(ctx); err != nil {
		return "", fmt.Errorf("failed copying staging object to production: %w", err)
	}

	if err := src.Delete(ctx); err != nil {
		log.Printf("Warning: Stale staging asset deletion missed: %v", err)
	}

	return productionPath, nil
}

func classifyDumpSite(ctx context.Context, gcsObjectPath string) (string, []string, float64, error) {
	bucketName := os.Getenv("BUCKET_NAME")
	imageURI := fmt.Sprintf("gs://%s/%s", bucketName, gcsObjectPath)

	client, err := vision.NewImageAnnotatorClient(ctx)
	if err != nil {
		return "", nil, 0, fmt.Errorf("failed to create vision client: %w", err)
	}
	defer client.Close()

	image := vision.NewImageFromURI(imageURI)
	labels, err := client.DetectLabels(ctx, image, nil, 12)
	if err != nil {
		return "", nil, 0, fmt.Errorf("vision label detection failed: %w", err)
	}

	var structuralWasteScore float64
	var trueSanitationBinScore float64

	subTypesMap := make(map[string]bool)
	var subTypes []string

	log.Println("--- Cloud Vision Labels Detected ---")
	for _, label := range labels {
		description := strings.ToLower(label.GetDescription())
		score := float64(label.GetScore())
		log.Printf("Label: %s, Confidence: %.2f%%\n", description, score*100)

		if strings.Contains(description, "dumpster") ||
			strings.Contains(description, "waste bin") ||
			strings.Contains(description, "trash bin") ||
			strings.Contains(description, "wheelie bin") ||
			description == "waste container" {
			if score > trueSanitationBinScore {
				trueSanitationBinScore = score
			}
		}

		if strings.Contains(description, "waste") ||
			strings.Contains(description, "litter") ||
			strings.Contains(description, "garbage") ||
			strings.Contains(description, "refuse") ||
			strings.Contains(description, "pollution") ||
			strings.Contains(description, "rubbish") {
			if score > structuralWasteScore {
				structuralWasteScore = score
			}
		}

		if strings.Contains(description, "plastic") || strings.Contains(description, "bag") || strings.Contains(description, "bottle") || strings.Contains(description, "polystyrene") {
			subTypesMap["Plastic Waste"] = true
		}
		if strings.Contains(description, "medical") || strings.Contains(description, "syringe") || strings.Contains(description, "needle") || strings.Contains(description, "bandage") || strings.Contains(description, "clinical") {
			subTypesMap["Medical Garbage"] = true
		}
		if strings.Contains(description, "sanitary") || strings.Contains(description, "diaper") || strings.Contains(description, "pad") || strings.Contains(description, "wipe") {
			subTypesMap["Sanitary Products"] = true
		}
		if strings.Contains(description, "construction") || strings.Contains(description, "brick") || strings.Contains(description, "cement") || strings.Contains(description, "concrete") || strings.Contains(description, "rubble") || strings.Contains(description, "debris") {
			subTypesMap["Construction Garbage"] = true
		}
	}

	for key := range subTypesMap {
		subTypes = append(subTypes, key)
	}
	if len(subTypes) == 0 && (trueSanitationBinScore > 0 || structuralWasteScore > 0) {
		subTypes = append(subTypes, "Mixed Unclassified Waste")
	}

	if trueSanitationBinScore > 0 && trueSanitationBinScore >= structuralWasteScore {
		return "regulated", subTypes, trueSanitationBinScore, nil
	}
	if structuralWasteScore > 0 {
		return "unregulated", subTypes, structuralWasteScore, nil
	}

	return "unknown", nil, 0, nil
}

func requestPreciseLocation(chatID int64) {
	promptText := "Got the image! 📍 Please tap the button below to share the exact coordinates of this location so we can log it properly."
	msg := tgbotapi.NewMessage(chatID, promptText)

	button := tgbotapi.NewKeyboardButtonLocation("📍 Share Current Location")
	keyboard := tgbotapi.NewReplyKeyboard([]tgbotapi.KeyboardButton{button})
	keyboard.OneTimeKeyboard = true
	keyboard.ResizeKeyboard = true

	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendOnboardingWelcome(chatID int64) {
	welcomeText := `📡 *BinItRadar Active*

*Why we exist:* To eliminate illegal urban garbage accumulation before it impacts public health. This bot crowdsources real-time visual proof to map unregulated waste hazards for municipal visibility.

📍 *To Report:* Simply send a photo of a waste pile. The system will analyze the image and request your precise coordinates to lock it to the map.

_Type /help to review data quality and anti-mischief rules._`

	msg := tgbotapi.NewMessage(chatID, welcomeText)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func sendReadmeHelp(chatID int64) {
	readmeText := `📋 *BinItRadar Reporting Rules*

Text chat is disabled. Follow these rules to avoid an automatic ban:

✅ *EXPECTED:*
• Clear photos of litter or trash bins.
• Precision GPS location shared within 5 mins when prompted.

❌ *NOT EXPECTED:*
• No text chatter, comments, or questions.
• No selfies, random objects, or scenery.

⚠️ *Notice:* Falsified images or text spam will trigger an *immediate automated ban* from the platform database.`

	msg := tgbotapi.NewMessage(chatID, readmeText)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func spawnDeferredGCSPurge(objectPath string, chatID int64, targetMessageID int) {
	go func() {
		time.Sleep(310 * time.Second)

		ctx := context.Background()
		collection := mongoClient.Database(databaseName).Collection(collectionName)

		var doc bson.M
		err := collection.FindOne(ctx, bson.M{
			"chatId":    chatID,
			"messageId": targetMessageID,
			"city":      "Unverified",
		}).Decode(&doc)

		if err != nil || doc != nil {
			log.Printf("Autonomous Cleanup: Session timed out for Message %d. Cleaning stranded asset: %s", targetMessageID, objectPath)

			bucketName := os.Getenv("BUCKET_NAME")
			storageClient, storageErr := storage.NewClient(ctx)
			if storageErr != nil {
				log.Printf("Cleanup Error: Failed to instantiate storage client: %v", storageErr)
				return
			}
			defer storageClient.Close()

			if deleteErr := storageClient.Bucket(bucketName).Object(objectPath).Delete(ctx); deleteErr != nil {
				log.Printf("Cleanup Notice: Staging asset already processed or missing: %v", deleteErr)
			}
		}
	}()
}

func handleReportsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	query := r.URL.Query()
	state := query.Get("state")
	city := query.Get("city")
	suburb := query.Get("suburb")
	street := query.Get("street")
	dumpType := query.Get("dumpType")

	if state == "" || city == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "state and city parameters are required"})
		return
	}

	filter := bson.M{
		"state": bson.M{"$eq": state},
		"city":  bson.M{"$eq": city},
	}

	if suburb != "" && suburb != "All" {
		filter["suburb"] = bson.M{"$eq": suburb}
	}
	if street != "" && street != "All" {
		filter["street"] = bson.M{"$eq": street}
	}
	if dumpType != "" && dumpType != "All" {
		filter["dumpType"] = bson.M{"$eq": dumpType}
	}
	filter["city"] = bson.M{"$ne": "Unverified"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := mongoClient.Database(databaseName).Collection(collectionName)
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	results := make([]DumpSiteData, 0)
	if err := cursor.All(ctx, &results); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for i := range results {
		signedURL, err := generateV4ReadSignedURL(results[i].ObjectName)
		if err != nil {
			log.Printf("Security Signature Block Failed for asset %s: %v", results[i].ObjectName, err)
			continue
		}
		results[i].ObjectName = signedURL
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func handleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	var update tgbotapi.Update
	err := json.NewDecoder(r.Body).Decode(&update)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	ctx := context.Background()
	db := mongoClient.Database(databaseName)
	collection := db.Collection(collectionName)
	blocklistCollection := db.Collection(blocklistCollName)

	var blockedUser BlockedUserData
	blockCheckCtx, blockCancel := context.WithTimeout(ctx, 3*time.Second)
	defer blockCancel()
	err = blocklistCollection.FindOne(blockCheckCtx, bson.M{"chatId": chatID}).Decode(&blockedUser)
	if err == nil {
		blockNotice := tgbotapi.NewMessage(chatID, "🚫 You have been blocked because you sent a message that does not conform to the standards. Please contact the administrator to unblock your profile.")
		bot.Send(blockNotice)
		w.WriteHeader(http.StatusOK)
		return
	}

	if update.Message.IsCommand() {
		command := update.Message.Command()
		if command == "start" {
			sendOnboardingWelcome(chatID)
			w.WriteHeader(http.StatusOK)
			return
		}
		if command == "help" {
			sendReadmeHelp(chatID)
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	if len(update.Message.Photo) > 0 {
		log.Printf("Photo payload intercepted from ChatID: %d", chatID)
		photoArray := update.Message.Photo
		targetPhoto := photoArray[len(photoArray)-1]

		gcsPath, err := processIncomingPhoto(ctx, targetPhoto.FileID)
		if err != nil {
			log.Printf("GCS upload failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		dumpType, subTypes, confidence, err := classifyDumpSite(ctx, gcsPath)
		if err != nil || dumpType == "unknown" {
			log.Printf("Mischief Action Triggered: Non-garbage asset uploaded by ChatID %d.", chatID)

			blockRecord := BlockedUserData{
				ChatID:    chatID,
				Reporter:  update.Message.From.UserName,
				BlockedOn: time.Now(),
				Reason:    "Uploaded non-conforming, non-waste visual imagery assets.",
			}
			writeBlockCtx, cancelBlock := context.WithTimeout(ctx, 3*time.Second)
			_, _ = blocklistCollection.InsertOne(writeBlockCtx, blockRecord)
			cancelBlock()

			bucketName := os.Getenv("BUCKET_NAME")
			storageClient, storageErr := storage.NewClient(ctx)
			if storageErr == nil {
				_ = storageClient.Bucket(bucketName).Object(gcsPath).Delete(ctx)
				storageClient.Close()
			}

			banNotice := tgbotapi.NewMessage(chatID, "🚫 You have been blocked because you sent a message that does not conform to the standards. Please contact the administrator to unblock your profile.")
			bot.Send(banNotice)
			w.WriteHeader(http.StatusOK)
			return
		}

		initialReport := DumpSiteData{
			ChatID:             chatID,
			MessageID:          update.Message.MessageID,
			Reporter:           update.Message.From.UserName,
			DumpType:           dumpType,
			ClassificationConf: confidence,
			GarbageSubTypes:    subTypes,
			ObjectName:         gcsPath,
			RecordedOn:         time.Now(),
			City:               "Unverified",
		}

		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = collection.InsertOne(writeCtx, initialReport)

		spawnDeferredGCSPurge(gcsPath, chatID, update.Message.MessageID)

		var confirmationText string
		materialsMatched := strings.Join(subTypes, ", ")
		if dumpType == "regulated" {
			confirmationText = fmt.Sprintf("✅ BinItBot Analysis: **Regulated Dump Site** (Bins found).\n🎯 Material Content: [%s]\n📊 Confidence: %.1f%%.", materialsMatched, confidence*100)
		} else {
			confirmationText = fmt.Sprintf("⚠️ BinItBot Analysis: **Unregulated Litter Accumulation**.\n🎯 Material Content: [%s]\n📊 Confidence: %.1f%%.", materialsMatched, confidence*100)
		}

		statusMsg := tgbotapi.NewMessage(chatID, confirmationText)
		statusMsg.ParseMode = "Markdown"
		bot.Send(statusMsg)

		requestPreciseLocation(chatID)
		w.WriteHeader(http.StatusOK)
		return
	}

	if update.Message.Location != nil {
		locPayload := update.Message.Location

		if locPayload.HorizontalAccuracy > 40.0 {
			rejectMsg := tgbotapi.NewMessage(chatID, "⚠️ **GPS Signal Weak:** Precision rejected.")
			bot.Send(rejectMsg)
			w.WriteHeader(http.StatusOK)
			return
		}

		var stagingDoc struct {
			ID         interface{} `bson:"_id"`
			ObjectName string      `bson:"objectName"`
		}

		findCtx, findCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer findCancel()

		err := collection.FindOne(findCtx,
			bson.M{"chatId": chatID, "city": "Unverified"},
			options.FindOne().SetSort(bson.M{"recordedOn": -1})).Decode(&stagingDoc)

		if err != nil {
			log.Printf("Session lookup failed or expired: %v", err)
			timeoutMsg := tgbotapi.NewMessage(chatID, "⚠️ **Session Expired:** The location request timed out. Please upload the photo again.")
			bot.Send(timeoutMsg)
			w.WriteHeader(http.StatusOK)
			return
		}

		productionPath, err := promoteGCSObject(ctx, stagingDoc.ObjectName)
		if err != nil {
			log.Printf("GCS Object promotion failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		geoData, err := reverseGeocode(locPayload.Latitude, locPayload.Longitude)

		state := "Unknown"
		county := "Unknown"
		stateDistrict := "Unknown"
		city := "Unknown"
		postcode := "Unknown"
		district := "Unknown"
		neighbourhood := "Unknown"
		suburb := "Unknown"
		street := "Unknown"

		if err == nil && len(geoData.Features) > 0 {
			props := geoData.Features[0].Properties
			if props.State != "" {
				state = props.State
			}
			if props.County != "" {
				county = props.County
			}
			if props.StateDistrict != "" {
				stateDistrict = props.StateDistrict
			}
			if props.City != "" {
				city = props.City
			}
			if props.Postcode != "" {
				postcode = props.Postcode
			}
			if props.District != "" {
				district = props.District
			}
			if props.Neighbourhood != "" {
				neighbourhood = props.Neighbourhood
			}
			if props.Suburb != "" {
				suburb = props.Suburb
			}
			if props.Street != "" {
				street = props.Street
			}
		}

		updateFilter := bson.M{"_id": stagingDoc.ID}
		updateQuery := bson.M{
			"$set": bson.M{
				"latitude":           locPayload.Latitude,
				"longitude":          locPayload.Longitude,
				"horizontalAccuracy": locPayload.HorizontalAccuracy,
				"objectName":         productionPath,
				"state":              state,
				"county":             county,
				"state_district":     stateDistrict,
				"city":               city,
				"postalCode":         postcode,
				"district":           district,
				"neighbourhood":      neighbourhood,
				"suburb":             suburb,
				"street":             street,
			},
		}

		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err = collection.UpdateOne(writeCtx, updateFilter, updateQuery)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		successMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🎉 **Report Archived Successfully!** Located at %s, %s (PIN: %s).", street, suburb, postcode))
		successMsg.ParseMode = "Markdown"
		successMsg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
		bot.Send(successMsg)

		w.WriteHeader(http.StatusOK)
		return
	}

	fallbackNotice := tgbotapi.NewMessage(chatID, "💡 **Unsupported Interaction:** Text conversation is restricted. Only reporting features are supported. Send an image of a waste site or type /help to review the standards.")
	bot.Send(fallbackNotice)
	w.WriteHeader(http.StatusOK)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	initMongoDB()
	initTelegram()
	defer func() {
		if err := mongoClient.Disconnect(context.Background()); err != nil {
			log.Printf("Error disconnecting from MongoDB: %v", err)
		}
	}()

	// ─── STATIC ROUTING HANDLING EXTENSION ───
	http.HandleFunc("/webhook", handleTelegramWebhook)
	http.HandleFunc("/api/reports", handleReportsAPI)

	// Mount the web directory onto the root path fallback router
	fs := http.FileServer(http.Dir("web"))
	http.Handle("/", fs)

	log.Fatal(http.ListenAndServe(":"+port, nil))
}

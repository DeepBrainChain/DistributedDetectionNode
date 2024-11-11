package db

import (
	"context"
	"errors"
	"time"

	"health-monitoring/log"
	"health-monitoring/types"

	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var MDB *mongoDB = nil

type mongoDB struct {
	Mongo                  *mongo.Client
	deviceOnlineCollection *mongo.Collection
	deviceInfoCollection   *mongo.Collection
}

func InitMongo(ctx context.Context, uri, db string, eas int64) error {
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI(uri).SetServerAPIOptions(serverAPI)
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		log.Log.Fatalf("Connect mongodb failed: %v", err)
		return err
	}
	if err = client.Ping(ctx, nil); err != nil {
		log.Log.Fatalf("Ping mongodb failed: %v", err)
		return err
	}
	MDB = &mongoDB{
		Mongo: client,
	}

	cl, err := client.Database(db).ListCollectionNames(ctx, bson.M{"name": "device_info"})
	if err != nil {
		log.Log.Fatalf("List mongodb collection names failed: %v", err)
		return err
	}
	if len(cl) == 0 {
		// Create collection with time series for device info
		tsOpts := options.TimeSeries()
		tsOpts.SetTimeField("timestamp")
		tsOpts.SetMetaField("device")
		tsOpts.SetGranularity("minutes")
		// tsOpts.SetBucketMaxSpan(30)
		// tsOpts.SetBucketRounding(5)
		ccOpts := options.CreateCollection()
		ccOpts.SetTimeSeriesOptions(tsOpts)
		ccOpts.SetExpireAfterSeconds(eas)
		if err := client.Database(db).CreateCollection(ctx, "device_info", ccOpts); err != nil {
			log.Log.Fatalf("Create time series collection failed: %v", err)
			return err
		}
		log.Log.Info("Create collection with time series success")
	}

	MDB.deviceOnlineCollection = client.Database(db).Collection("device_online")
	MDB.deviceInfoCollection = client.Database(db).Collection("device_info")
	return nil
}

func (db *mongoDB) Disconnect(ctx context.Context) {
	if err := db.Mongo.Disconnect(ctx); err != nil {
		panic(err)
	}
}

func (db *mongoDB) IsNodeOnline(ctx context.Context, nodeId string) bool {
	result := types.MDBDeviceOnline{}
	if err := db.deviceOnlineCollection.FindOne(ctx, bson.M{"device_id": nodeId}).Decode(&result); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false
		}
		return false
	}
	return true
}

func (db *mongoDB) NodeOnline(ctx context.Context, nodeId string) error {
	res, err := db.deviceOnlineCollection.InsertOne(ctx, types.MDBDeviceOnline{
		DeviceId: nodeId,
		AddTime:  time.Now(),
	})
	if err != nil {
		log.Log.WithFields(logrus.Fields{"node_id": nodeId}).Error("insert online failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"node_id": nodeId}).Info("inserted online id ", res.InsertedID)
	return nil
}

func (db *mongoDB) NodeOffline(ctx context.Context, nodeId string) error {
	result, err := db.deviceOnlineCollection.DeleteOne(ctx, bson.M{"device_id": nodeId})
	if err != nil {
		log.Log.WithFields(logrus.Fields{"node_id": nodeId}).Error("delete online failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"node_id": nodeId}).Info("delete online count ", result.DeletedCount)
	return nil
}

func (db *mongoDB) GetDeviceInfo(ctx context.Context, nodeId string) (*types.MDBDeviceInfo, error) {
	result := &types.MDBDeviceInfo{}
	if err := db.deviceInfoCollection.FindOne(ctx, bson.M{"device_id": nodeId}).Decode(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (db *mongoDB) AddDeviceInfo(ctx context.Context, nodeId string, tm time.Time, info types.WsMachineInfoRequest) error {
	result, err := db.deviceInfoCollection.InsertOne(
		ctx,
		types.MDBDeviceInfo{
			Timestamp: tm,
			Device: types.MDBMetaField{
				DeviceId: nodeId,
				Project:  info.Project,
				Models:   info.Models,
				GPUName:  info.GPUName,
			},
			UtilizationGPU: info.UtilizationGPU,
			MemoryTotal:    info.MemoryTotal,
			MemoryUsed:     info.MemoryUsed,
		},
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"node_id": nodeId}).Error("insert device info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"node_id": nodeId}).Info("inserted device info id ", result.InsertedID)
	return nil
}

func (db *mongoDB) DeleteExpiredDeviceInfo(ctx context.Context, tm time.Time) error {
	result, err := db.deviceInfoCollection.DeleteMany(
		ctx,
		bson.M{
			"timestamp": bson.M{"$lt": tm},
		},
	)
	if err != nil {
		log.Log.Errorf("Delete expired documents before %v manully failed: %v", tm, err)
		return err
	}
	log.Log.Infof("Delete expired documents before %v manully DeletedCount %v", tm, result.DeletedCount)
	return nil
}

func (db *mongoDB) GetAllLatestDeviceInfo(ctx context.Context) []types.MDBDeviceInfo {
	di := make([]types.MDBDeviceInfo, 0)
	pipeline := mongo.Pipeline{
		// {{"$match", bson.D{{"timestamp", bson.D{{"$gt", specificTimestamp}}}}}},
		{{"$sort", bson.D{{"device.device_id", 1}, {"timestamp", -1}}}},
		{{"$group", bson.D{
			{"_id", "$device.device_id"},
			{"latestRecord", bson.D{{"$first", "$$ROOT"}}},
		}}},
		{{"$replaceRoot", bson.D{{"newRoot", "$latestRecord"}}}},
	}
	cursor, err := db.deviceInfoCollection.Aggregate(ctx, pipeline)
	if err != nil {
		log.Log.Errorf("Aggregate documents of all latest device info failed: %v", err)
		return di
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		result := types.MDBDeviceInfo{}
		if err := cursor.Decode(&result); err != nil {
			log.Log.Errorf("Decode aggregate cursor into struct failed: %v", err)
		} else {
			di = append(di, result)
		}
	}
	if err := cursor.Err(); err != nil {
		log.Log.Errorf("Traversal aggregate cursor failed: %v", err)
	}
	return di
}

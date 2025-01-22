package db

import (
	"context"
	"errors"
	"time"

	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"

	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var MDB *mongoDB = nil

type mongoDB struct {
	Mongo                   *mongo.Client
	machineOnlineCollection *mongo.Collection
	machineInfoCollection   *mongo.Collection
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

	cl, err := client.Database(db).ListCollectionNames(ctx, bson.M{"name": "machine_info"})
	if err != nil {
		log.Log.Fatalf("List mongodb collection names failed: %v", err)
		return err
	}
	if len(cl) == 0 {
		// Create collection with time series for machine info
		tsOpts := options.TimeSeries()
		tsOpts.SetTimeField("timestamp")
		tsOpts.SetMetaField("machine")
		tsOpts.SetGranularity("minutes")
		// tsOpts.SetBucketMaxSpan(30)
		// tsOpts.SetBucketRounding(5)
		ccOpts := options.CreateCollection()
		ccOpts.SetTimeSeriesOptions(tsOpts)
		ccOpts.SetExpireAfterSeconds(eas)
		if err := client.Database(db).CreateCollection(ctx, "machine_info", ccOpts); err != nil {
			log.Log.Fatalf("Create time series collection failed: %v", err)
			return err
		}
		log.Log.Info("Create collection with time series success")
	}

	MDB.machineOnlineCollection = client.Database(db).Collection("machine_online")
	MDB.machineInfoCollection = client.Database(db).Collection("machine_info")
	return nil
}

func (db *mongoDB) Disconnect(ctx context.Context) {
	if err := db.Mongo.Disconnect(ctx); err != nil {
		panic(err)
	}
}

func (db *mongoDB) IsMachineOnline(ctx context.Context, machine types.MachineKey) bool {
	result := types.MDBMachineOnline{}
	if err := db.machineOnlineCollection.FindOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	).Decode(&result); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false
		}
		return false
	}
	return true
}

func (db *mongoDB) MachineOnline(ctx context.Context, machine types.MachineKey) error {
	res, err := db.machineOnlineCollection.InsertOne(ctx, types.MDBMachineOnline{
		MachineKey: machine,
		AddTime:    time.Now(),
	})
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("insert online failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("inserted online id ", res.InsertedID)
	return nil
}

func (db *mongoDB) MachineOffline(ctx context.Context, machine types.MachineKey) error {
	result, err := db.machineOnlineCollection.DeleteOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("delete online failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("delete online count ", result.DeletedCount)
	return nil
}

func (db *mongoDB) GetMachineInfo(ctx context.Context, machine types.MachineKey) (*types.MDBMachineInfo, error) {
	result := &types.MDBMachineInfo{}
	if err := db.machineInfoCollection.FindOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	).Decode(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (db *mongoDB) AddMachineInfo(ctx context.Context, machine types.MachineKey, tm time.Time, info types.WsMachineInfoRequest) error {
	result, err := db.machineInfoCollection.InsertOne(
		ctx,
		types.MDBMachineInfo{
			Timestamp: tm,
			Machine: types.MDBMetaField{
				MachineKey: machine,
			},
			GPUNames: info.GPUNames,
			// UtilizationGPU: info.UtilizationGPU,
			MemoryTotal: info.MemoryTotal,
			// MemoryUsed:     info.MemoryUsed,
		},
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("insert machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("inserted machine info id ", result.InsertedID)
	return nil
}

func (db *mongoDB) DeleteExpiredMachineInfo(ctx context.Context, tm time.Time) error {
	result, err := db.machineInfoCollection.DeleteMany(
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

func (db *mongoDB) GetAllLatestmachineInfo(ctx context.Context) []types.MDBMachineInfo {
	di := make([]types.MDBMachineInfo, 0)
	pipeline := mongo.Pipeline{
		// {{"$match", bson.D{{"timestamp", bson.D{{"$gt", specificTimestamp}}}}}},
		{{"$sort", bson.D{{"machine.machine_id", 1}, {"timestamp", -1}}}},
		{{"$group", bson.D{
			{"_id", "$machine.machine_id"},
			{"latestRecord", bson.D{{"$first", "$$ROOT"}}},
		}}},
		{{"$replaceRoot", bson.D{{"newRoot", "$latestRecord"}}}},
	}
	cursor, err := db.machineInfoCollection.Aggregate(ctx, pipeline)
	if err != nil {
		log.Log.Errorf("Aggregate documents of all latest machine info failed: %v", err)
		return di
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		result := types.MDBMachineInfo{}
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

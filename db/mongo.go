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
	machineTMCollection     *mongo.Collection
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

	cl, err := client.Database(db).ListCollectionNames(ctx, bson.M{"name": "machine_tm"})
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
		if err := client.Database(db).CreateCollection(ctx, "machine_tm", ccOpts); err != nil {
			log.Log.Fatalf("Create time series collection failed: %v", err)
			return err
		}
		log.Log.Info("Create collection with time series success")
	}

	MDB.machineOnlineCollection = client.Database(db).Collection("machine_online")
	MDB.machineInfoCollection = client.Database(db).Collection("machine_info")
	MDB.machineTMCollection = client.Database(db).Collection("machine_tm")
	return nil
}

func DisconnectMongo(ctx context.Context) error {
	return MDB.Mongo.Disconnect(ctx)
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

	update, err := db.machineInfoCollection.UpdateOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
		bson.M{
			"$set": bson.M{
				"last_offline_time": time.Now(),
			},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("update last offline time failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("update last offline time count ", update.ModifiedCount)
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

func (db *mongoDB) RegisterMachine(ctx context.Context, machine types.MachineKey) error {
	res, err := db.machineInfoCollection.InsertOne(ctx, types.MDBMachineInfo{
		MachineKey:   machine,
		CalcPoint:    0,
		RegisterTime: time.Now(),
	})
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("insert machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("inserted machine info id ", res.InsertedID)
	return nil
}

func (db *mongoDB) UnregisterMachine(ctx context.Context, machine types.MachineKey) error {
	result, err := db.machineInfoCollection.DeleteOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("delete machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("delete machine info count ", result.DeletedCount)
	return nil
}

func (db *mongoDB) SetMachineInfo(
	ctx context.Context,
	machine types.MachineKey,
	info types.WsMachineInfoRequest,
	calcPoint float64,
	longitude, latitude float32,
) error {
	update, err := db.machineInfoCollection.UpdateOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
		bson.M{
			"$set": bson.M{
				"gpu_names":        info.GPUNames,
				"gpu_memory_total": info.GPUMemoryTotal,
				"memory_total":     info.MemoryTotal,
				"cpu_type":         info.CpuType,
				"cpu_rate":         info.CpuRate,
				"wallet":           info.Wallet,
				"client_ip":        info.ClientIP,
				"calc_point":       calcPoint,
				"longitude":        longitude,
				"latitude":         latitude,
			},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("set machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("set machine info count ", update.ModifiedCount)
	return nil
}

func (db *mongoDB) AddMachineTM(ctx context.Context, machine types.MachineKey, tm time.Time, info types.WsMachineInfoRequest) error {
	result, err := db.machineTMCollection.InsertOne(
		ctx,
		types.MDBMachineTM{
			Timestamp:   tm,
			Machine:     machine,
			MachineInfo: types.MachineInfo(info),
		},
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("insert machine tm info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("inserted machine tm info id ", result.InsertedID)
	return nil
}

func (db *mongoDB) DeleteExpiredMachineTM(ctx context.Context, tm time.Time) error {
	result, err := db.machineTMCollection.DeleteMany(
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

func (db *mongoDB) GetAllLatestMachineTM(ctx context.Context) []types.MDBMachineTM {
	di := make([]types.MDBMachineTM, 0)
	pipeline := mongo.Pipeline{
		// {{"$match", bson.D{{"timestamp", bson.D{{"$gt", specificTimestamp}}}}}},
		{{"$sort", bson.D{{"machine.machine_id", 1}, {"timestamp", -1}}}},
		{{"$group", bson.D{
			{"_id", "$machine.machine_id"},
			{"latestRecord", bson.D{{"$first", "$$ROOT"}}},
		}}},
		{{"$replaceRoot", bson.D{{"newRoot", "$latestRecord"}}}},
	}
	cursor, err := db.machineTMCollection.Aggregate(ctx, pipeline)
	if err != nil {
		log.Log.Errorf("Aggregate documents of all latest machine info failed: %v", err)
		return di
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		result := types.MDBMachineTM{}
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

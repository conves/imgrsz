package internal

import (
	"log"
	"time"
)

// FileWatchingWorker watches for new images and push them into the processing queue
type FileWatchingWorker struct {
	queue    ProcessingQueue
	store    ImageStore
	pubsub   ImageProcessedAckBus
	basepath string
}

func NewFileWatchingWorker(queue ProcessingQueue, store ImageStore,
	pubsub ImageProcessedAckBus, basepath string) *FileWatchingWorker {
	return &FileWatchingWorker{
		queue:    queue,
		store:    store,
		pubsub:   pubsub,
		basepath: basepath,
	}
}

func (w FileWatchingWorker) Do() {
	for {
		count, err := w.store.Count()
		if err != nil {
			log.Printf("failed to count images: %s\n", err)
			continue
		}

		currentImages.Set(float64(count))

		// todo decide whether we need to do something with the new images or not
		//imgs, err := w.store.LoadNew()
		//if err != nil {
		//	log.Printf("failed to read new images for processing: %s\n", err)
		//	time.Sleep(time.Millisecond * 100)
		//	continue
		//}
		//
		//for _, img := range imgs {
		//	if err := w.store.Save(img, nil); err != nil {
		//		log.Printf("failed to enqueue a new image for processing: %s\n", err)
		//	}
		//}

		time.Sleep(time.Millisecond * 100)
	}
}

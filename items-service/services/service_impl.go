package services

import (
	"context"
	"fmt"
	"io"
	"items/clients/queue"
	"items/dtos"
	"items/services/repositories"
	e "items/utils/errors/errors"
	"net/http"
	"os"
	"path/filepath"
)

type ServiceImpl struct {
	localCache repositories.Repository
	distCache  repositories.Repository
	db         repositories.Repository
	queue      queue.Publisher
}

func NewServiceImpl(
	localCache repositories.Repository,
	distCache repositories.Repository,
	db repositories.Repository,
	queue queue.Publisher,
) *ServiceImpl {
	return &ServiceImpl{
		localCache: localCache,
		distCache:  distCache,
		db:         db,
		queue:      queue,
	}
}

func (serv *ServiceImpl) GetItemById(ctx context.Context, id string) (dtos.ItemDto, e.ApiError) {
	var item dtos.ItemDto
	var items dtos.ItemsDto
	var apiErr e.ApiError
	var source string

	items = append(items, item)

	// try to find it in localCache
	item, apiErr = serv.localCache.GetItemById(ctx, id)
	if apiErr != nil {
		if apiErr.Status() != http.StatusNotFound {
			return dtos.ItemDto{}, apiErr
		}
		// try to find it in distCache
		item, apiErr = serv.distCache.GetItemById(ctx, id)
		if apiErr != nil {
			if apiErr.Status() != http.StatusNotFound {
				return dtos.ItemDto{}, apiErr
			}
			// try to find it in db
			item, apiErr = serv.db.GetItemById(ctx, id)
			if apiErr != nil {
				if apiErr.Status() != http.StatusNotFound {
					return dtos.ItemDto{}, apiErr
				} else {
					fmt.Printf("Not found item %s in any datasource\n", id)
					apiErr = e.NewNotFoundApiError(fmt.Sprintf("item %s not found", id))
					return dtos.ItemDto{}, apiErr
				}
			} else {
				source = "db"
				defer func() {
					if _, apiErr := serv.distCache.InsertItems(ctx, items); apiErr != nil {
						fmt.Printf("Error trying to save item in distCache %v\n", apiErr)
					}
					if _, apiErr := serv.localCache.InsertItems(ctx, items); apiErr != nil {
						fmt.Printf("Error trying to save item in localCache %v\n", apiErr)
					}
				}()
			}
		} else {
			source = "distCache"
			defer func() {
				if _, apiErr := serv.localCache.InsertItems(ctx, items); apiErr != nil {
					fmt.Printf("Error trying to save item in localCache %v\n", apiErr)
				}
			}()
		}
	} else {
		source = "localCache"
	}

	fmt.Printf("Obtained item from %s!\n", source)
	return item, nil
}

func (serv *ServiceImpl) InsertItems(ctx context.Context, items dtos.ItemsDto) (dtos.ItemsDto, e.ApiError) {
	results, apiErr := serv.db.InsertItems(ctx, items)
	if apiErr != nil {
		fmt.Printf("Error inserting item in db: %v\n", apiErr)
		return dtos.ItemsDto{}, apiErr
	}
	fmt.Printf("Inserted item in db: %v\n", results)

	_, apiErr = serv.distCache.InsertItems(ctx, results)
	if apiErr != nil {
		fmt.Printf("Error inserting item in distCache: %v\n", apiErr)
		return results, nil
	}
	fmt.Printf("Inserted item in distCache: %v\n", results)

	_, apiErr = serv.localCache.InsertItems(ctx, results)
	if apiErr != nil {
		fmt.Printf("Error inserting item in localCache: %v\n", apiErr)
		return results, nil
	}
	fmt.Printf("Inserted item in localCache: %v\n", results)

	for _, item := range results {
		if err := serv.queue.PublishInsert(ctx, item); err != nil {
			return results, e.NewInternalServerApiError(fmt.Sprintf("Error publishing item %s", item.Id), err)
		}
		fmt.Printf("Message sent: %v\n", item.Id)

		go downloadImage(item.Picture, item.Id, "/home")
	}

	return results, nil
}

func (serv *ServiceImpl) UpdateItem(ctx context.Context, item dtos.ItemDto) (dtos.ItemDto, e.ApiError) {
	result, apiErr := serv.db.UpdateItem(ctx, item)
	if apiErr != nil {
		fmt.Printf("Error updating item in db: %v\n", apiErr)
		return dtos.ItemDto{}, apiErr
	}
	fmt.Printf("Updated item in db: %v\n", result)

	_, apiErr = serv.distCache.UpdateItem(ctx, result)
	if apiErr != nil {
		fmt.Printf("Error updating item in distCache: %v\n", apiErr)
		return result, nil
	}
	fmt.Printf("updated item in distCache: %v\n", result)

	_, apiErr = serv.localCache.UpdateItem(ctx, result)
	if apiErr != nil {
		fmt.Printf("Error updating item in localCache: %v\n", apiErr)
		return result, nil
	}
	fmt.Printf("updated item in localCache: %v\n", result)

	if err := serv.queue.PublishUpdate(ctx, item); err != nil {
		return result, e.NewInternalServerApiError(fmt.Sprintf("Error publishing item %s", item.Id), err)
	}
	fmt.Printf("Message sent: %v\n", item.Id)

	go downloadImage(item.Picture, item.Id, "/home")

	return result, nil
}

func downloadImage(url, name, folder string) error {
	// Crear la ruta completa de la imagen

	name = "image-" + name
	filePath := filepath.Join(folder, name)

	// Hacer una solicitud HTTP GET a la URL de la imagen
	response, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("error image request HTTP: %s", err)
	}
	defer response.Body.Close()

	// Crear el archivo en la carpeta de destino
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file: %s", err)
	}
	defer file.Close()

	// Copiar el contenido de la respuesta HTTP al archivo

	_, err = io.Copy(file, response.Body)
	if err != nil {
		return fmt.Errorf("error saving image: %s", err)
	}

	fmt.Printf("%s downloaded succesfully! \n", name)
	return nil
}

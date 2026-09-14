package source

import (
	"context"
	"errors"
	"fmt"
	"regtool/source/localdata"
	"regtool/source/structs"
)

//here is the command implementation of the source

// Check if there is support registry
func Update(updateChan chan string) error {

	managers := GetAllRegisteredApp()
	res := make(map[string]string)
	var errs []error
	for name, manager := range managers {
		current, err := manager.GetCurrRegistry()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read current %s registry: %w", name, err))
			continue
		}
		res[name] = current
		updateChan <- name + " is updated"
	}

	if err := localdata.SaveToBackup(res); err != nil {
		errs = append(errs, fmt.Errorf("failed to save registry backup: %w", err))
	}

	return errors.Join(errs...)
}

func ChangeAllRegistry(region string, updateChan chan string) error {
	regionValue, ok := structs.StringToRegion(region)
	if !ok {
		return fmt.Errorf("unknown region: %s", region)
	}

	ctx := context.Background()

	rs, err := LoadRegistrySources(ctx)
	if err != nil {
		return fmt.Errorf("failed to load registry sources: %w", err)
	}

	localAppsMap, err := localdata.ReadBackupFile()
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	appManagers := GetAllRegisteredApp()
	//TODO do backup if changed
	//lets do a git log-like backup for chang every time
	var errs []error
	for name := range localAppsMap {
		manager, ok := appManagers[name]
		if !ok {
			errs = append(errs, fmt.Errorf("no registry manager found for %q", name))
			continue
		}

		if _, err := manager.SetRegistry(regionValue, rs); err != nil {
			errs = append(errs, fmt.Errorf("failed to set %s registry to region %s: %w", name, region, err))
		}
	}

	return errors.Join(errs...)
}

func ListAllRegistry(ch chan<- string) {
	rs, err := LoadRegistrySources(context.Background())
	if err != nil {
		ch <- fmt.Sprintf("ERROR: Failed to get registry sources: %s", err.Error())
		return
	}

	res := make(map[string][]Source)
	for region, regionSources := range *rs {
		for appName, urls := range regionSources {
			for _, url := range urls {
				res[appName] = append(res[appName], Source{
					Region: string(region),
					Url:    url,
					Name:   appName,
				})
			}
		}
	}

	for appName, sources := range res {
		ch <- fmt.Sprintf("APP: %s", appName)
		for _, source := range sources {
			ch <- fmt.Sprintf("  REGION: %s, URL: %s", source.Region, source.Url)
		}
		ch <- "\n"
	}
}
func ListRegistryByAppName(appName string, ch chan<- string) {
	rs, err := LoadRegistrySources(context.Background())
	if err != nil {
		ch <- fmt.Sprintf("ERROR: Failed to get registry sources: %s", err.Error())
		close(ch)
		return
	}

	res := make(map[string][]Source)
	for region, regionSources := range *rs {
		for name, urls := range regionSources {
			for _, url := range urls {
				res[name] = append(res[name], Source{
					Region: string(region),
					Url:    url,
					Name:   name,
				})
			}
		}
	}

	if sources, found := res[appName]; found {
		ch <- fmt.Sprintf("APP: %s", appName)
		for _, source := range sources {
			ch <- fmt.Sprintf("REGION: %s, URL: %s", source.Region, source.Url)
		}
	} else {
		ch <- fmt.Sprintf("ERROR: No sources found for app: %s", appName)
	}

	close(ch)
}

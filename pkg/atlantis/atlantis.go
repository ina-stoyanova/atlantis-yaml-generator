package atlantis

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/totmicro/atlantis-yaml-generator/pkg/config"
	"github.com/totmicro/atlantis-yaml-generator/pkg/helpers"
	"gopkg.in/yaml.v3"
)

const tfvarsExtension = ".tfvars"

type Config struct {
	Version       int       `yaml:"version"`
	Automerge     bool      `yaml:"automerge"`
	ParallelApply bool      `yaml:"parallel_apply"`
	ParallelPlan  bool      `yaml:"parallel_plan"`
	Projects      []Project `yaml:"projects"`
}

type Project struct {
	Name      string `yaml:"name"`
	Workspace string `yaml:"workspace"`
	Workflow  string `yaml:"workflow"`
	Dir       string `yaml:"dir"`
	Autoplan  struct {
		Enabled      bool     `yaml:"enabled"`
		WhenModified []string `yaml:"when_modified"`
	} `yaml:"autoplan"`
}

type ProjectFolder struct {
	Path          string
	WorkspaceList []string
}

// GenerateAtlantisYAML generates the atlantis.yaml file
func GenerateAtlantisYAML(prChangedFiles []string) error {
	// Get the changed files from the PR

	// Scan folders to detect projects
	projectFoldersList, err := scanProjectFolders(
		config.GlobalConfig.Parameters["terraform-base-dir"],
		config.GlobalConfig.Parameters["workflow"],
		config.GlobalConfig.Parameters["pattern-detector"],
		prChangedFiles)
	if err != nil {
		return err
	}
	// Detect project workspaces
	projectFoldersListWithWorkspaces, err := detectProjectWorkspaces(
		projectFoldersList,
		config.GlobalConfig.Parameters["workflow"],
		config.GlobalConfig.Parameters["pattern-detector"],
		prChangedFiles)
	if err != nil {
		return err
	}
	// Generate atlantis projects
	atlantisProjects, err := generateAtlantisProjects(
		config.GlobalConfig.Parameters["workflow"],
		projectFoldersListWithWorkspaces)
	if err != nil {
		return err
	}
	// Filter atlantis projects with included and excluded regex rules
	filteredAtlantisProjects, err := filterAtlantisProjects(
		config.GlobalConfig.Parameters["excluded-projects"],
		config.GlobalConfig.Parameters["included-projects"],
		atlantisProjects)
	if err != nil {
		return err
	}
	// Generate atlantis config to later render the atlantis.yaml file
	atlantisConfig, err := generateAtlantisConfig(
		config.GlobalConfig.Parameters["automerge"],
		config.GlobalConfig.Parameters["parallel-apply"],
		config.GlobalConfig.Parameters["parallel-plan"],
		config.GlobalConfig.Parameters["when-modified"],
		filteredAtlantisProjects)
	if err != nil {
		return err
	}
	// Generate atlantis.yaml file
	err = generateOutputYAML(&atlantisConfig,
		config.GlobalConfig.Parameters["output-file"],
		config.GlobalConfig.Parameters["output-type"])
	if err != nil {
		return err
	}
	return nil
}

func scanProjectFolders(basePath, workflow, patternDetector string, changedFiles []string) (projectFolders []ProjectFolder, err error) {
	// Scan folders for projects and apply filters
	err = filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		relPath, _ := filepath.Rel(basePath, filepath.Dir(path))
		// Detect projects folders based on the workflow
		workflowFilterResult := workflowFilter(info, path, workflow, patternDetector)
		// Filter projects based on the PR changed files
		prFilterResult := prFilter(relPath, changedFiles)
		if workflowFilterResult && prFilterResult {
			projectFolders = append(projectFolders, ProjectFolder{
				Path: relPath,
			})
		}
		return nil
	})
	return projectFolders, err
}

func detectProjectWorkspaces(foldersList []ProjectFolder, workflow string, patternDetector string, changedFiles []string) (updatedFoldersList []ProjectFolder, err error) {
	// Detect project workspaces based on the workflow
	switch workflow {
	case "single-workspace":
		updatedFoldersList, err = singleWorkspaceDetectProjectWorkspaces(foldersList)
	case "multi-workspace":
		updatedFoldersList, err = multiWorkspaceDetectProjectWorkspaces(changedFiles, foldersList, patternDetector)
	}
	// You can add more workflows rules here if required
	return updatedFoldersList, err
}

func generateAtlantisProjects(workflow string, projectFolderList []ProjectFolder) (projects []Project, err error) {
	// Iterate over the project folders and generate atlantis projects
	for _, folder := range projectFolderList {
		for _, workspace := range folder.WorkspaceList {
			name := genProjectName(folder.Path, workspace)
			projects = append(projects, Project{
				Name:      name,
				Dir:       folder.Path,
				Workspace: workspace,
				Workflow:  workflow,
			})
		}
	}
	return projects, nil
}

func filterAtlantisProjects(excludedProjects, includedProjects string, atlantisProjects []Project) (filteredAtlantisProjects []Project, err error) {

	// Iterate over atlantis projects and filter them
	for _, project := range atlantisProjects {
		projectFilterResult, err := projectFilter(project.Name, excludedProjects, includedProjects)
		if err != nil {
			return filteredAtlantisProjects, err
		}
		if projectFilterResult {
			filteredAtlantisProjects = append(filteredAtlantisProjects, project)
		}
	}
	return filteredAtlantisProjects, nil
}

func generateAtlantisConfig(autoMerge, parallelApply, parallelPlan, whenModified string, projects []Project) (Config, error) {
	// Parse atlantis parameters to detect config values
	automerge, err := strconv.ParseBool(autoMerge)
	if err != nil {
		return Config{}, err
	}
	parallelapply, err := strconv.ParseBool(parallelApply)
	if err != nil {
		return Config{}, err
	}
	parallelplan, err := strconv.ParseBool(parallelPlan)
	if err != nil {
		return Config{}, err
	}
	whenmodified := strings.Split(whenModified, ",")
	// Generate the atlantis base config
	config := Config{
		Version:       3,
		Automerge:     automerge,
		ParallelApply: parallelapply,
		ParallelPlan:  parallelplan,
	}
	// Append generated projects to the atlantis config
	for _, info := range projects {
		project := Project{
			Name:      info.Name,
			Workspace: info.Workspace,
			Workflow:  info.Workflow,
			Dir:       info.Dir,
			Autoplan: struct {
				Enabled      bool     `yaml:"enabled"`
				WhenModified []string `yaml:"when_modified"`
			}{
				Enabled:      true,
				WhenModified: whenmodified,
			},
		}
		config.Projects = append(config.Projects, project)
	}
	return config, err
}

func generateOutputYAML(config *Config, outputFile string, outputType string) error {
	// Generate the atlantis.yaml file
	yamlBytes, err := yaml.Marshal(&config)
	if err != nil {
		return err
	}
	switch outputType {
	case "file":
		err = helpers.WriteFile(string(yamlBytes), outputFile)
		return err
	case "stdout":
		fmt.Printf(string(yamlBytes))
		return nil
	default:
		return fmt.Errorf("output type '%s' is not supported", outputType)
	}
}

func workflowFilter(info os.FileInfo, path, workflow, patternDetector string) bool {
	// Detect projects folders based on the workflow
	// Each workflow has different rules to detect projects
	switch workflow {
	case "single-workspace":
		return singleWorkspaceWorkflowFilter(info, path, patternDetector)
	case "multi-workspace":
		return multiWorkspaceWorkflowFilter(info, path, patternDetector)
	}
	// You can add more workflows rules here if required
	return true
}

func prFilter(relPath string, changedFiles []string) bool {
	// Filter projects based on the PR changed files
	for _, file := range changedFiles {
		if strings.HasPrefix(file, fmt.Sprintf("%s/", relPath)) {
			return true
		}
	}
	return false
}

func genProjectName(path, workspace string) string {
	// Generate project name based on the path and workspace
	if workspace != "default" {
		return fmt.Sprintf("%s-%s", strings.Replace(path, "/", "-", 1), workspace)
	}
	return strings.Replace(path, "/", "-", 1)
}

func projectFilter(item, excludes, includes string) (result bool, err error) {
	// If the regexp is not defined, we don't filter the project
	if includes == "" && excludes == "" {
		return true, nil
	}
	// Compile the regular expressions
	var patternInclude, patternExclude *regexp.Regexp
	if includes != "" {
		patternInclude, err = regexp.Compile(includes)
	}
	if err != nil {
		return false, err
	}
	if excludes != "" {
		patternExclude, err = regexp.Compile(excludes)
	}
	if err != nil {
		return false, err
	}
	// Check if the item matches the include and exclude patterns
	if patternInclude != nil && !patternInclude.MatchString(item) {
		return false, nil
	}
	if patternExclude != nil && patternExclude.MatchString(item) {
		return false, nil
	}
	return true, nil
}

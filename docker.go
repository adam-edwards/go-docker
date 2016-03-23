/**
 * TODO:
 * - Add error types for comparisons
 */

package docker

import (
  "os"
  "os/exec"
  "time"
  "strings"
  "fmt"
  "io"
  "bufio"
  "regexp"
)


const DEF_DOCKERFILE_NAME = "Dockerfile"
const DEF_REGISTRY_HOST   = "docker.io"

type DockerClient struct {
  Command             string
  Dockerfile          string
  RegistryHost        string
  ParentContainerId   string
  IsInContainer       bool
  ShowOutput          bool
}


/**
 * Helper function to get the current working dir
 */
func getwd() (wd string, err error) {
  if wd, err = os.Getwd(); err != nil {
    return "", fmt.Errorf("Unable to determine working directory")
  }
  return
}


/**
 * Run a shell command
 */
func runCommand(
  showOutput bool,
  workdir string,
  command string,
  args ...string) (command_string, output string, err error) {

  command_parts := append([]string{command}, args...)
  command_string = strings.Join(command_parts, " ")
  cmd := exec.Command(command, args...)
  cmd.Dir = workdir

  // Setup pipes for stdout and stderr
  so, err := cmd.StdoutPipe()
  if err != nil { return command_string, "", err }
  se, err := cmd.StderrPipe()
  if err != nil { return command_string, "", err }

  // Execute command
  if err := cmd.Start(); err != nil { return command_string, "", err }

  // Setup reader and channels
  reader  := io.MultiReader(so, se)
  scanner := bufio.NewScanner(reader)
  out     := make(chan string, 1)
  timeout := make(chan bool)

  // Read pipes, print to stdout
  go func() {
    var t string
    for scanner.Scan() {
      t = scanner.Text()
      out<- t
      if showOutput { fmt.Println(t) }
    }
    close(out)
  }()

  // Poll Docker daemon to make sure it's still alive
  go func() {
    dc, err := NewClient()
    if err != nil { timeout<- true }
    for {
      if (! dc.IsConnected()) {
        timeout<- true
        return
      }
      time.Sleep(5 * time.Second)
    }
  }()

  // Collect output, return error if Docker daemon stops responding
  Loop:
  for {
    select {
    case o, chOpen := <-out:
      output = output + o
      if !chOpen { break Loop }
    case to, _ := <-timeout:
      if to {
        err = fmt.Errorf("Lost connection to Docker daemon")
        return
      }
    }
  }

  err = cmd.Wait()
  return
}


/**
 * Check the /proc/1/cgroup file to see if we're
 * running inside a container. If so, return the
 * ID of the parent container
 */
func getParentContainerId() (id string, err error) {
  file, err := os.Open("/proc/1/cgroup")
  if err != nil { return "", err }
  defer file.Close()

  s := bufio.NewScanner(file)
  re := regexp.MustCompile(`(:/docker/)([a-z0-9]+$)`)

  for s.Scan() {
    // Look for a container ID
    matches := re.FindStringSubmatch(s.Text())
    if len(matches) == 3 {
      id = matches[2]
      return id, nil
    }
  }

  err = s.Err()
  return
}


/**
 * Instantiate and return the Docker client
 */
func NewClient() (dc *DockerClient, err error) {
  // Make sure docker command is in $PATH
  p, err := exec.LookPath("docker")
  if err != nil { return nil, err }

  // If we're running inside a container, get its ID
  cid, cidErr := getParentContainerId()
  inContainer := (cidErr == nil)

  // Create and returnt the client
  dc = &DockerClient{
    Command: p,
    Dockerfile: DEF_DOCKERFILE_NAME,
    RegistryHost: DEF_REGISTRY_HOST,
    ParentContainerId: cid,
    IsInContainer: inContainer,
    ShowOutput: false,
  }
  return
}


/**
 * Return wether or not the Docker client
 * is connected to the Docker daemon
 */
func (d *DockerClient) IsConnected() bool {
  cmd := exec.Command(d.Command, "info")
  err := cmd.Run()
  return err == nil
}


/**
 * Return the version of the Docker client
 */
func (d *DockerClient) Version() string {
  // TODO: To be implemented
  return ""
}


/**
 * Execute a 'docker build' command
 */
func (d *DockerClient) Build(
  imgName, buildContextDir string,
  args ...string ) (cmd_string, output string, err error) {

  // Build arg array
  cmd_args := []string{
    "build",
    "-f", d.Dockerfile,
    "-t", imgName,
  }
  cmd_args = append(cmd_args, args...)
  cmd_args = append(cmd_args, ".")

  return runCommand(d.ShowOutput, buildContextDir, d.Command, cmd_args...)
}


/**
 * Execute a 'docker run' command
 */
func (d *DockerClient) Run(
  imgName string,
  command, volumes, envVars []string,
  args ...string ) (cmd_string, output string, err error) {

  cmd_args := []string{ "run" }
  // Add volumes and env vars to the arg array
  for _, v := range volumes {
    cmd_args = append(cmd_args, "-v", v)
  }
  for _, e := range envVars {
    cmd_args = append(cmd_args, "-e", e)
  }
  // Add args, image namd and command to arg array
  cmd_args = append(cmd_args, args...)
  cmd_args = append(cmd_args, imgName)
  cmd_args = append(cmd_args, command...)

  wd, err := getwd()
  if err != nil { return "", "", err }
  return runCommand(d.ShowOutput, wd, d.Command, cmd_args...)
}


/**
 * Push an image to a Docker registry server
 */
func (d *DockerClient) Push(imgName string) (output string, err error) {
  // If RegistryHost is not the default, prepend it to the image name
  if d.RegistryHost != DEF_REGISTRY_HOST {
    imgName = d.RegistryHost + "/" + imgName
  }
  cmd := exec.Command(d.Command, "push", imgName)
  out, err := cmd.CombinedOutput()
  output = string(out)
  return
}


/**
 * Pull an image from a Docker registry server
 */
func (d *DockerClient) Pull(imgName string) (output string, err error) {
  // If RegistryHost is not the default, prepend it to the image name
  if d.RegistryHost != DEF_REGISTRY_HOST {
    imgName = d.RegistryHost + "/" + imgName
  }
  cmd := exec.Command(d.Command, "pull", imgName)
  out, err := cmd.CombinedOutput()
  output = string(out)
  return
}


/**
 * Get an image ID from name and tag
 */
func (d *DockerClient) getImageID(imgName string) (imgID string, err error) {
  cmd := exec.Command(d.Command, "images", "-q", imgName)
  o, err := cmd.Output()
  if err != nil { return "", err }

  // Make sure we only have one ID to return
  if strings.Count(string(o), "\n") > 1 {
    return "", fmt.Errorf("Multiple IDs returned for image: %s", imgName)
  }

  imgID = strings.Trim(string(o), "\n ")
  return
}


/**
 * Tag docker image
 */
func (d *DockerClient) Tag(imgName, newTag string) (err error) {
  imgID, err := d.getImageID(imgName)
  if err != nil { return err }

  cmd := exec.Command(d.Command, "tag", "-f", imgID, newTag)
  err = cmd.Run()
  return err
}
